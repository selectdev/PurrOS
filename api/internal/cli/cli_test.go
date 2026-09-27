package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/selectdev/purros/api/internal/cli"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/secure"
	"github.com/selectdev/purros/api/internal/storage"
	"github.com/selectdev/purros/api/internal/storage/storagetest"
	"github.com/selectdev/purros/api/internal/testutil"
)

// run executes purros with the given database and returns stdout, stderr and
// the exit code.
func run(t *testing.T, dbURL, stdin string, args ...string) (string, string, int) {
	t.Helper()
	t.Setenv("DATABASE_URL", dbURL)
	if os.Getenv("PURROS_SECRET") == "" {
		t.Setenv("PURROS_SECRET", testutil.TestSecret)
	}
	t.Setenv("PURROS_URL", "https://erp.example.com")
	var out, errOut bytes.Buffer
	code := cli.RunWith(args, strings.NewReader(stdin), &out, &errOut)
	return out.String(), errOut.String(), code
}

func mustRun(t *testing.T, dbURL, stdin string, args ...string) string {
	t.Helper()
	out, errOut, code := run(t, dbURL, stdin, args...)
	if code != 0 {
		t.Fatalf("purros %s: exit %d\n%s%s", strings.Join(args, " "), code, out, errOut)
	}
	return out
}

func TestSetup(t *testing.T) {
	url := testutil.EmptyDatabase(t)
	// Missing values without a terminal.
	if _, errOut, code := run(t, url, "", "setup", "--company", "X"); code == 0 || !strings.Contains(errOut, "--owner-email") {
		t.Fatalf("expected missing flag error: %s", errOut)
	}
	out := mustRun(t, url, "", "--json", "setup", "--company", "Acme", "--owner-email", "owner@example.com", "--timezone", "Europe/London",
		"--location-name", "Shop 1", "--location-external-id", "S1")
	var res map[string]string
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if !strings.HasPrefix(res["signInLink"], "https://erp.example.com/invitation?token=") || res["locationId"] == "" {
		t.Fatalf("setup result: %v", res)
	}
	if _, errOut, code := run(t, url, "", "setup", "--company", "Again", "--owner-email", "x@example.com"); code == 0 || !strings.Contains(errOut, "already been run") {
		t.Fatalf("second setup: %s", errOut)
	}
}

func TestSetupWizard(t *testing.T) {
	url := testutil.EmptyDatabase(t)
	answers := strings.Join([]string{"Wizard Co", "Nowhere/Invalid", "America/New_York", "usd", "EUR", "not-an-email", "boss@example.com",
		"Pat", "y", "Main Street", "", "", "25:00", "03:30"}, "\n") + "\n"
	out := mustRun(t, url, answers, "--interactive", "setup")
	for _, want := range []string{"enter a time zone", "3-letter currency", "valid email", "enter a time like", `Location "Main Street" created`,
		"https://erp.example.com/invitation?token="} {
		if !strings.Contains(out, want) {
			t.Fatalf("wizard output missing %q:\n%s", want, out)
		}
	}
	loc := mustRun(t, url, "", "--json", "locations", "list")
	if !strings.Contains(loc, `"timezone": "America/New_York"`) {
		t.Fatalf("location: %s", loc)
	}
}

func TestRecovery(t *testing.T) {
	env := testutil.New(t, []string{"organization:read"}, nil)
	u := env.DatabaseURL
	env.OwnerID() // owner@test is active with a password

	// Lock the owner out, then recover.
	if _, err := env.Pool.Exec(context.Background(), `UPDATE users SET locked_until = now() + interval '1 hour', totp_enabled_at = now(), totp_secret = 'x'`); err != nil {
		t.Fatal(err)
	}
	if _, _, code := run(t, u, "", "recover", "owner", "owner@test"); code == 0 {
		t.Fatal("recover must ask for confirmation without a terminal")
	}
	out := mustRun(t, u, "", "-y", "recover", "owner", "owner@test")
	if !strings.Contains(out, "/reset-password?token=") {
		t.Fatalf("recover: %s", out)
	}
	show := mustRun(t, u, "", "--json", "users", "show", "owner@test")
	if !strings.Contains(show, `"mfaEnabled": false`) || !strings.Contains(show, `"lockedUntil": null`) {
		t.Fatalf("owner not recovered: %s", show)
	}
	// The last Owner can't be demoted or deactivated.
	if _, errOut, code := run(t, u, "", "-y", "users", "set-role", "owner@test", "--role", "employee"); code == 0 || !strings.Contains(errOut, "last Owner") {
		t.Fatalf("demote last owner: %s", errOut)
	}
	if _, errOut, code := run(t, u, "", "users", "deactivate", "owner@test"); code == 0 || !strings.Contains(errOut, "last Owner") {
		t.Fatalf("deactivate last owner: %s", errOut)
	}
	// Create a second Owner from scratch, then the first can be demoted.
	mustRun(t, u, "", "-y", "recover", "owner", "second@example.com", "--create", "--name", "Sam")
	mustRun(t, u, "", "-y", "users", "set-role", "owner@test", "--role", "Employee")
	list := mustRun(t, u, "", "users", "list", "--role", "owner")
	if !strings.Contains(list, "second@example.com") || strings.Contains(list, "owner@test") {
		t.Fatalf("owners: %s", list)
	}
	invite := mustRun(t, u, "", "--json", "users", "invite", "new@example.com", "--name", "New Person")
	if !strings.Contains(invite, "invitationUrl") {
		t.Fatalf("invite: %s", invite)
	}
	mustRun(t, u, "", "users", "unlock", "new@example.com")
	mustRun(t, u, "", "users", "sign-out", "--email", "new@example.com")
	if !strings.Contains(mustRun(t, u, "", "roles", "list"), "Owner") {
		t.Fatal("roles list")
	}
	// Recovery is audited.
	var n int
	_ = env.Pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action IN ('recover.owner', 'user.set_role', 'user.invite')`).Scan(&n)
	if n < 4 {
		t.Fatalf("audit entries: %d", n)
	}
}

func TestBackupRestoreCommands(t *testing.T) {
	env := testutil.New(t, []string{"people:write", "people:read"}, nil)
	env.Do("POST", "/api/v1/employees", map[string]any{"firstName": "Kept", "lastName": "Person"}).Expect(t, 201)
	dir := t.TempDir()
	passFile := filepath.Join(dir, "pass")
	_ = os.WriteFile(passFile, []byte("a long backup passphrase\n"), 0o600)

	out := mustRun(t, env.DatabaseURL, "", "--json", "backup", "create", "--dir", dir, "--passphrase-file", passFile)
	var b struct {
		Path      string `json:"path"`
		Encrypted bool   `json:"encrypted"`
		Rows      int64  `json:"rows"`
	}
	if err := json.Unmarshal([]byte(out), &b); err != nil || !b.Encrypted || b.Rows == 0 {
		t.Fatalf("create: %v %s", err, out)
	}
	if _, errOut, code := run(t, env.DatabaseURL, "", "backup", "verify", b.Path, "--no-input"); code == 0 || !strings.Contains(errOut, "passphrase") {
		t.Fatalf("verify without passphrase: %s", errOut)
	}
	mustRun(t, env.DatabaseURL, "", "backup", "verify", b.Path, "--passphrase-file", passFile)
	if out := mustRun(t, env.DatabaseURL, "", "backup", "inspect", b.Path, "--passphrase-file", passFile); !strings.Contains(out, "matches this PURROS_SECRET") {
		t.Fatalf("inspect: %s", out)
	}

	// Restore into an empty database, then over one with data.
	empty := testutil.EmptyDatabase(t)
	mustRun(t, empty, "", "-y", "backup", "restore", b.Path, "--passphrase-file", passFile)
	if out := mustRun(t, empty, "", "--json", "status"); !strings.Contains(out, `"employees": 1`) {
		t.Fatalf("restored status: %s", out)
	}
	if _, errOut, code := run(t, empty, "", "-y", "backup", "restore", b.Path, "--passphrase-file", passFile); code == 0 || !strings.Contains(errOut, "--replace") {
		t.Fatalf("restore over data without --replace: %s", errOut)
	}
	out = mustRun(t, empty, "", "-y", "backup", "restore", b.Path, "--passphrase-file", passFile, "--replace", "--safety-dir", dir)
	if !strings.Contains(out, "pre-restore") {
		t.Fatalf("expected a safety backup: %s", out)
	}
	listed := mustRun(t, empty, "", "backup", "list", "--dir", dir)
	if strings.Count(listed, ".purros-backup") < 2 {
		t.Fatalf("list: %s", listed)
	}
	// Restoring with a different secret warns and needs confirmation.
	t.Setenv("PURROS_SECRET", strings.Repeat("z", 40))
	if _, errOut, code := run(t, empty, "", "backup", "restore", b.Path, "--passphrase-file", passFile, "--replace"); code == 0 || !strings.Contains(errOut, "--yes") {
		t.Fatalf("different secret should need confirmation: %s", errOut)
	}
	mustRun(t, empty, "", "backup", "prune", "--dir", dir, "--keep", "1")
	if files, _ := filepath.Glob(filepath.Join(dir, "*.purros-backup")); len(files) != 1 {
		t.Fatalf("prune left %v", files)
	}
}

func TestSecretRotation(t *testing.T) {
	env := testutil.New(t, []string{"organization:read"}, []string{"employee.created"}) // registers a webhook secret
	u := env.DatabaseURL
	mustRun(t, u, "", "secret", "check")
	newSecret := strings.Repeat("n", 40)
	file := filepath.Join(t.TempDir(), "new")
	_ = os.WriteFile(file, []byte(newSecret+"\n"), 0o600)
	out := mustRun(t, u, "", "-y", "secret", "rotate", "--new-secret-file", file)
	if !strings.Contains(out, "Re-encrypted 2 value(s)") || !strings.Contains(out, secure.Fingerprint(newSecret)) {
		t.Fatalf("rotate: %s", out)
	}
	if _, errOut, code := run(t, u, "", "secret", "check"); code == 0 || !strings.Contains(errOut, "can't be read") {
		t.Fatalf("old secret should no longer work: %s", errOut)
	}
	t.Setenv("PURROS_SECRET", newSecret)
	mustRun(t, u, "", "secret", "check")
}

func TestStatusDoctorMigrate(t *testing.T) {
	env := testutil.New(t, []string{"organization:read", "inventory:write"}, nil)
	u := env.DatabaseURL
	var st map[string]any
	if err := json.Unmarshal([]byte(mustRun(t, u, "", "--json", "status")), &st); err != nil {
		t.Fatal(err)
	}
	if st["company"] != "Test Co" || int64(st["schemaVersion"].(float64)) != db.LatestVersion() {
		t.Fatalf("status: %v", st)
	}
	out, _, code := run(t, u, "", "doctor")
	if code != 0 || !strings.Contains(out, "✓ encryption secret") || !strings.Contains(out, "✓ stock ledger") {
		t.Fatalf("doctor (exit %d): %s", code, out)
	}
	// A broken ledger fails the doctor.
	ctx := context.Background()
	env.Do("POST", "/api/v1/items", map[string]any{"sku": "X", "name": "X"}).Expect(t, 201)
	if _, err := env.Pool.Exec(ctx, `INSERT INTO stock_levels (item_id, location_id, on_hand) SELECT id, $1, 5 FROM items LIMIT 1`, env.LocationID); err != nil {
		t.Fatal(err)
	}
	if out, _, code := run(t, u, "", "doctor"); code != 1 || !strings.Contains(out, "don't match the ledger") {
		t.Fatalf("doctor should fail on drift (exit %d): %s", code, out)
	}
	if out := mustRun(t, u, "", "migrate", "status"); !regexp.MustCompile(`00006_attachments\.sql\s+applied`).MatchString(out) {
		t.Fatalf("migrate status: %s", out)
	}
	mustRun(t, u, "", "migrate", "--dry-run")
	if out := mustRun(t, u, "", "version"); !strings.HasPrefix(out, "purros ") {
		t.Fatalf("version: %s", out)
	}
}

func TestInitAndEnvFile(t *testing.T) {
	dir := t.TempDir()
	out := mustRun(t, "", "", "init", "--dir", dir, "--url", "https://erp.example.com")
	if !strings.Contains(out, "Wrote") {
		t.Fatal(out)
	}
	path := filepath.Join(dir, ".env")
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	first, _ := os.ReadFile(path)
	if _, _, code := run(t, "", "", "init", "--dir", dir); code == 0 {
		t.Fatal("init overwrote .env without --force")
	}
	mustRun(t, "", "", "init", "--dir", dir, "--force", "--url", "https://other.example.com")
	second, _ := os.ReadFile(path)
	secret := func(b []byte) string {
		for _, l := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(l, "PURROS_SECRET=") {
				return l
			}
		}
		return ""
	}
	if secret(first) == "" || secret(first) != secret(second) || !strings.Contains(string(second), "other.example.com") {
		t.Fatal("init --force must keep the secret")
	}
	// --env-file loads values without overriding the environment.
	t.Setenv("PURROS_URL", "")
	os.Unsetenv("PURROS_URL")
	os.Unsetenv("LOG_LEVEL")
	envFile := filepath.Join(dir, "custom.env")
	_ = os.WriteFile(envFile, []byte("# comment\nexport LOG_LEVEL=debug\nPURROS_BACKUP_DIR=\"/srv/backups\" \nSMTP_FROM='Ops <ops@example.com>'\n"), 0o600)
	var outBuf, errBuf bytes.Buffer
	t.Setenv("DATABASE_URL", "postgres://x:y@localhost/z")
	if code := cli.RunWith([]string{"--env-file", envFile, "--json", "config", "show"}, strings.NewReader(""), &outBuf, &errBuf); code != 0 {
		t.Fatalf("config show: %s", errBuf.String())
	}
	var cfg map[string]string
	_ = json.Unmarshal(outBuf.Bytes(), &cfg)
	if cfg["LOG_LEVEL"] != "debug" || cfg["PURROS_BACKUP_DIR"] != "/srv/backups" || cfg["SMTP_FROM"] != "Ops <ops@example.com>" ||
		strings.Contains(cfg["DATABASE_URL"], ":y@") {
		t.Fatalf("config: %v", cfg)
	}
	os.Unsetenv("PURROS_BACKUP_DIR")
	os.Unsetenv("SMTP_FROM")
	os.Unsetenv("LOG_LEVEL")
}

func TestBackupS3AndStorageCommands(t *testing.T) {
	env := testutil.New(t, []string{"people:write"}, nil)
	env.Do("POST", "/api/v1/employees", map[string]any{"firstName": "Cloud", "lastName": "Kept"}).Expect(t, 201)
	s3 := storagetest.S3(t, "backups")
	t.Setenv("PURROS_BACKUP_S3_ENABLED", "true")
	t.Setenv("PURROS_BACKUP_S3_BUCKET", s3.Bucket)
	t.Setenv("PURROS_BACKUP_S3_REGION", s3.Region)
	t.Setenv("PURROS_BACKUP_S3_ENDPOINT", s3.Endpoint)
	t.Setenv("PURROS_BACKUP_S3_ACCESS_KEY_ID", s3.AccessKeyID)
	t.Setenv("PURROS_BACKUP_S3_SECRET_ACCESS_KEY", s3.SecretAccessKey)
	t.Setenv("PURROS_BACKUP_S3_FORCE_PATH_STYLE", "true")
	t.Setenv("PURROS_BACKUP_DIR", "")
	t.Setenv("STORAGE_LOCAL_PATH", t.TempDir())

	// No local directory: the backup goes only to S3.
	out := mustRun(t, env.DatabaseURL, "", "--json", "backup", "create")
	var res struct {
		Path      string `json:"path"`
		RemoteKey string `json:"remoteKey"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.RemoteKey == "" || res.Path != "" {
		t.Fatalf("create: %v %s", err, out)
	}
	if list := mustRun(t, env.DatabaseURL, "", "backup", "list", "--remote"); !strings.Contains(list, "s3:"+res.RemoteKey) {
		t.Fatalf("remote list: %s", list)
	}
	mustRun(t, env.DatabaseURL, "", "backup", "verify", "s3:"+res.RemoteKey)

	empty := testutil.EmptyDatabase(t)
	mustRun(t, empty, "", "-y", "backup", "restore", "s3:"+res.RemoteKey)
	if st := mustRun(t, empty, "", "--json", "status"); !strings.Contains(st, `"employees": 1`) {
		t.Fatalf("restored: %s", st)
	}
	dir := t.TempDir()
	mustRun(t, empty, "", "backup", "download", res.RemoteKey, "--out", dir)
	if files, _ := filepath.Glob(filepath.Join(dir, "*.purros-backup")); len(files) != 1 {
		t.Fatalf("download: %v", files)
	}
	mustRun(t, empty, "", "backup", "prune", "--remote", "--keep", "1")

	if out := mustRun(t, empty, "", "storage", "test"); !strings.Contains(out, "works") {
		t.Fatal(out)
	}
	if out := mustRun(t, empty, "", "storage", "test", "--backups"); !strings.Contains(out, "s3://backups/purros-backups/") {
		t.Fatal(out)
	}
	mustRun(t, empty, "", "storage", "verify")
	if out, _, _ := run(t, empty, "", "doctor"); !strings.Contains(out, "✓ file storage") || !strings.Contains(out, "✓ backup bucket (S3)") {
		t.Fatalf("doctor: %s", out)
	}
}

func TestStorageMigrate(t *testing.T) {
	env := testutil.New(t, []string{"attachments:write"}, nil)
	local := t.TempDir()
	env.App.Storage = storage.NewLocal(local)
	req, _ := http.NewRequest("POST", env.Server.URL+"/api/v1/attachments?name=note.txt", strings.NewReader("proof of delivery"))
	req.Header.Set("Authorization", "Bearer "+env.Key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 201 {
		t.Fatalf("upload: %v %v", err, resp)
	}
	resp.Body.Close()
	s3 := storagetest.S3(t, "files")
	t.Setenv("STORAGE_DRIVER", "local")
	t.Setenv("STORAGE_LOCAL_PATH", local)
	t.Setenv("STORAGE_S3_BUCKET", s3.Bucket)
	t.Setenv("STORAGE_S3_REGION", s3.Region)
	t.Setenv("STORAGE_S3_ENDPOINT", s3.Endpoint)
	t.Setenv("STORAGE_S3_ACCESS_KEY_ID", s3.AccessKeyID)
	t.Setenv("STORAGE_S3_SECRET_ACCESS_KEY", s3.SecretAccessKey)
	t.Setenv("STORAGE_S3_FORCE_PATH_STYLE", "true")
	if out := mustRun(t, env.DatabaseURL, "", "storage", "migrate", "--to", "s3"); !strings.Contains(out, "Copied 1 file(s)") {
		t.Fatal(out)
	}
	if out := mustRun(t, env.DatabaseURL, "", "storage", "migrate", "--to", "s3"); !strings.Contains(out, "1 already there") {
		t.Fatal(out)
	}
	t.Setenv("STORAGE_DRIVER", "s3")
	if out := mustRun(t, env.DatabaseURL, "", "storage", "verify", "--checksums"); !strings.Contains(out, "0 problem(s)") {
		t.Fatal(out)
	}
}
