package server_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/selectdev/purros/api/internal/backup"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/testutil"
)

func TestBackupRoundTrip(t *testing.T) {
	src := testutil.New(t, everyScope, nil)
	ctx := context.Background()
	boss := src.Do("POST", "/api/v1/employees", map[string]any{"firstName": "Boss", "lastName": "B", "homeLocationId": src.LocationID}).Expect(t, 201).Str("id")
	src.Do("POST", "/api/v1/employees", map[string]any{"firstName": "Tab\tNew\nLine \\ é", "lastName": "C", "managerId": boss}).Expect(t, 201)
	sup := src.Do("POST", "/api/v1/suppliers", map[string]any{"name": "Beans Co"}).Expect(t, 201).Str("id")
	itm := src.Do("POST", "/api/v1/items", map[string]any{"sku": "BEANS", "name": "Beans"}).Expect(t, 201).Str("id")
	po := src.Do("POST", "/api/v1/purchase-orders", map[string]any{"supplierId": sup, "locationId": src.LocationID,
		"lines": []any{map[string]any{"itemId": itm, "quantity": "10", "unitPrice": "2"}}}).Expect(t, 201)
	src.OwnerID()

	dir := t.TempDir()
	path := filepath.Join(dir, backup.FileName(time.Now(), backup.KindManual))
	res, err := backup.ToFile(ctx, src.Pool, path, backup.KindManual, backup.Options{Passphrase: "open sesame", PurrOSVersion: "test", SecretFingerprint: "fp"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Manifest.SchemaVersion != db.LatestVersion() || res.Manifest.Company != "Test Co" || res.Size == 0 {
		t.Fatalf("manifest: %+v", res.Manifest)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("backup file mode %v", st.Mode())
	}

	// Verification needs the passphrase and catches damage.
	if _, err := backup.Verify(path, ""); err == nil {
		t.Fatal("encrypted backup read without a passphrase")
	}
	if _, err := backup.Verify(path, "wrong"); !errors.Is(err, backup.ErrPassphrase) {
		t.Fatalf("wrong passphrase: %v", err)
	}
	m, err := backup.Verify(path, "open sesame")
	if err != nil || !m.Encrypted || m.Rows() == 0 {
		t.Fatalf("verify: %v %+v", err, m)
	}
	raw, _ := os.ReadFile(path)
	cut := filepath.Join(dir, "cut"+backup.Extension)
	_ = os.WriteFile(cut, raw[:len(raw)-100], 0o600)
	if _, err := backup.Verify(cut, "open sesame"); err == nil {
		t.Fatal("truncated backup accepted")
	}
	flipped := append([]byte(nil), raw...)
	flipped[len(flipped)/2] ^= 0xff
	bad := filepath.Join(dir, "bad"+backup.Extension)
	_ = os.WriteFile(bad, flipped, 0o600)
	if _, err := backup.Verify(bad, "open sesame"); err == nil {
		t.Fatal("tampered backup accepted")
	}

	// Restoring over data needs Replace.
	dst := testutil.New(t, everyScope, nil)
	if _, err := backup.Restore(ctx, dst.Pool, path, backup.RestoreOptions{Passphrase: "open sesame"}); !errors.Is(err, backup.ErrNotEmpty) {
		t.Fatalf("expected ErrNotEmpty, got %v", err)
	}
	var steps []string
	if _, err := backup.Restore(ctx, dst.Pool, path, backup.RestoreOptions{Passphrase: "open sesame", Replace: true,
		Progress: func(s string) { steps = append(steps, s) }}); err != nil {
		t.Fatal(err)
	}
	if len(steps) < 4 {
		t.Fatalf("steps: %v", steps)
	}

	// The restored database answers the API with the same data, keys included.
	dst.Key = src.Key
	list := dst.Do("GET", "/api/v1/employees", nil).Expect(t, 200)
	if n := len(list.Get("data").([]any)); n != 2 {
		t.Fatalf("employees: %s", list.Raw)
	}
	found := false
	for _, e := range list.Get("data").([]any) {
		if e.(map[string]any)["firstName"] == "Tab\tNew\nLine \\ é" {
			found = true
		}
	}
	if !found {
		t.Fatalf("special characters didn't survive: %s", list.Raw)
	}
	dst.Do("GET", "/api/v1/purchase-orders/"+po.Str("id"), nil).Expect(t, 200)
	// Sequences continue where they were.
	next := dst.Do("POST", "/api/v1/purchase-orders", map[string]any{"supplierId": sup, "locationId": src.LocationID,
		"lines": []any{map[string]any{"itemId": itm, "quantity": "1", "unitPrice": "2"}}}).Expect(t, 201)
	if next.Str("number") == po.Str("number") {
		t.Fatalf("sequence reset: %s", next.Raw)
	}
	dst.SignIn("owner@test", "")
	if v, _ := db.SchemaVersion(ctx, dst.Pool); v != db.LatestVersion() {
		t.Fatalf("schema version %d", v)
	}

	// Unencrypted backups, listing and pruning.
	for i := range 3 {
		p := filepath.Join(dir, backup.FileName(time.Now().Add(time.Duration(i)*time.Second), backup.KindScheduled))
		if _, err := backup.ToFile(ctx, src.Pool, p, backup.KindScheduled, backup.Options{}); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(p, time.Now().Add(time.Duration(i)*time.Minute), time.Now().Add(time.Duration(i)*time.Minute))
	}
	files, _ := backup.List(dir)
	if len(files) != 6 { // 1 encrypted + cut + bad + 3 plain
		t.Fatalf("list: %v", files)
	}
	removed, err := backup.Prune(dir, 2, 0, time.Now())
	if err != nil || len(removed) != 4 {
		t.Fatalf("prune: %v %v", removed, err)
	}
	runs, _ := backup.Last(ctx, src.Pool, 10)
	if len(runs) != 4 || runs[0].Status != "succeeded" {
		t.Fatalf("runs: %+v", runs)
	}
}

func TestScheduledBackup(t *testing.T) {
	env := testutil.New(t, everyScope, nil)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := backup.Schedule{Dir: t.TempDir(), HourUTC: 2, Keep: 2}
	early := time.Date(2026, 9, 27, 1, 0, 0, 0, time.UTC)
	if ran, _ := backup.RunScheduled(ctx, env.Pool, s, early, log); ran {
		t.Fatal("ran before the backup hour")
	}
	// Run records use the database clock, so "today" is the real today.
	s.HourUTC = 0
	due := time.Now().UTC()
	ran, err := backup.RunScheduled(ctx, env.Pool, s, due, log)
	if err != nil || !ran {
		t.Fatalf("first run: %v %v", ran, err)
	}
	if ran, _ := backup.RunScheduled(ctx, env.Pool, s, due.Add(time.Minute), log); ran {
		t.Fatal("ran twice in a day")
	}
	files, _ := backup.List(s.Dir)
	if len(files) != 1 {
		t.Fatalf("files: %v", files)
	}
	if _, err := backup.Verify(files[0].Path, ""); err != nil {
		t.Fatal(err)
	}
}
