package internal_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/selectdev/purros/api/internal/catalog"
	"github.com/selectdev/purros/api/internal/features"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/secure"
)

func TestFeatureDependents(t *testing.T) {
	deps := features.Dependents("people")
	for _, want := range []string{"people.documents", "time", "time.kiosk", "scheduling", "employee_area"} {
		if !slices.Contains(deps, want) {
			t.Errorf("Dependents(people) missing %s", want)
		}
	}
	if slices.Contains(deps, "sales") {
		t.Error("sales does not depend on people")
	}
	if d := features.Dependents("sales"); !slices.Contains(d, "cash") || !slices.Contains(d, "inventory.usage_recipes") {
		t.Errorf("Dependents(sales) = %v", d)
	}
}

func TestCatalogsReferenceRealFeatures(t *testing.T) {
	check := func(kind, key, feat string) {
		if feat == features.Core {
			return
		}
		if _, ok := features.Lookup(feat); !ok {
			t.Errorf("%s %s references unknown feature %s", kind, key, feat)
		}
	}
	for _, p := range catalog.Permissions {
		check("permission", p.Key, p.Feature)
	}
	for _, s := range catalog.Scopes {
		check("scope", s.Key, s.Feature)
	}
	for e, f := range catalog.Events {
		check("event", e, f)
	}
}

func TestWebhookSignature(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	body := []byte(`{"id":"evt_1"}`)
	sig := secure.SignWebhook("whsec_x", now, body)
	if err := secure.VerifyWebhook("whsec_x", sig, body, now, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	if secure.VerifyWebhook("whsec_y", sig, body, now, 5*time.Minute) == nil {
		t.Error("wrong secret accepted")
	}
	if secure.VerifyWebhook("whsec_x", sig, []byte(`{"id":"evt_2"}`), now, 5*time.Minute) == nil {
		t.Error("tampered body accepted")
	}
	if secure.VerifyWebhook("whsec_x", sig, body, now.Add(10*time.Minute), 5*time.Minute) == nil {
		t.Error("stale signature accepted")
	}
}

func TestBoxRoundTrip(t *testing.T) {
	box, err := secure.NewBox("a-very-long-master-secret-for-testing-only")
	if err != nil {
		t.Fatal(err)
	}
	sealed := box.Seal([]byte("hello"))
	plain, err := box.Open(sealed)
	if err != nil || string(plain) != "hello" {
		t.Fatalf("round trip: %q %v", plain, err)
	}
	other, _ := secure.NewBox("a-different-master-secret-for-testing-only")
	if _, err := other.Open(sealed); err == nil {
		t.Error("opened with the wrong key")
	}
}

func TestAPIKeyFormat(t *testing.T) {
	plain, hash, display := secure.NewAPIKey()
	if len(plain) != len(secure.APIKeyPrefix)+40 || string(secure.HashAPIKey(plain)) != string(hash) || plain[:len(display)] != display {
		t.Fatalf("unexpected key %q display %q", plain, display)
	}
}

func TestMemoryLimiter(t *testing.T) {
	l := httpx.NewMemoryLimiter()
	ctx := context.Background()
	for i := range 3 {
		r, _ := l.Allow(ctx, "k", 3, time.Minute)
		if !r.Allowed || r.Remaining != 2-i {
			t.Fatalf("request %d: %+v", i, r)
		}
	}
	if r, _ := l.Allow(ctx, "k", 3, time.Minute); r.Allowed {
		t.Fatal("4th request allowed")
	}
	if r, _ := l.Allow(ctx, "other", 3, time.Minute); !r.Allowed {
		t.Fatal("separate keys should have separate limits")
	}
}

func TestDateJSON(t *testing.T) {
	var d httpx.Date
	if err := d.UnmarshalJSON([]byte(`"2026-09-27"`)); err != nil || d.String() != "2026-09-27" {
		t.Fatalf("%v %s", err, d)
	}
	if err := d.UnmarshalJSON([]byte(`"27/09/2026"`)); err == nil {
		t.Fatal("accepted a non-ISO date")
	}
}
