package server_test

import (
	"archive/zip"
	"bytes"
	"context"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/selectdev/purros/api/internal/auth"
	"github.com/selectdev/purros/api/internal/catalog"
	"github.com/selectdev/purros/api/internal/cli"
	"github.com/selectdev/purros/api/internal/testutil"
)

var tokenRe = regexp.MustCompile(`token=([A-Za-z0-9]+)`)

// lastToken returns the token in the newest email of a kind.
func lastToken(t *testing.T, env *testutil.Env, kind string) string {
	t.Helper()
	emails := env.Emails()
	for _, email := range slices.Backward(emails) {
		if email["kind"] == kind {
			m := tokenRe.FindStringSubmatch(email["body"])
			if m == nil {
				t.Fatalf("no token in %s email: %s", kind, email["body"])
			}
			return m[1]
		}
	}
	t.Fatalf("no %s email in %v", kind, emails)
	return ""
}

func TestPermissionTableMatchesRoutes(t *testing.T) {
	env := testutil.New(t, everyScope, nil)
	routes := map[string]bool{}
	for _, r := range env.App.Router.Routes() {
		routes[r.Method+" "+r.Path] = true
	}
	perms := map[string]bool{"anyone": true, "-": true}
	for _, p := range catalog.Permissions {
		perms[p.Key] = true
	}
	for route, perm := range catalog.RoutePermissions {
		if !routes[route] {
			t.Errorf("permission table has a route that doesn't exist: %s", route)
		}
		if !perms[perm] {
			t.Errorf("%s maps to unknown permission %q", route, perm)
		}
	}
	for route := range catalog.RouteReach {
		if !routes[route] {
			t.Errorf("reach table has a route that doesn't exist: %s", route)
		}
	}
}

func TestSignInAndSessions(t *testing.T) {
	env := testutil.New(t, everyScope, nil)
	env.OwnerID()

	// Unauthenticated and wrong password.
	anon := env.NewSession()
	anon.Do("GET", "/api/v1/auth/session", nil).Expect(t, 401)
	anon.Do("POST", "/api/v1/auth/sign-in", map[string]any{"email": "owner@test", "password": "nope-nope-nope"}).Expect(t, 401)
	anon.Do("POST", "/api/v1/auth/sign-in", map[string]any{"email": "ghost@test", "password": "nope-nope-nope"}).Expect(t, 401)

	s := env.SignIn("OWNER@test", "")
	info := s.Do("GET", "/api/v1/auth/session", nil).Expect(t, 200)
	if info.Get("role.owner") != true || info.Str("user.email") != "owner@test" {
		t.Fatalf("session: %s", info.Raw)
	}
	// The Owner can use any endpoint.
	s.Do("GET", "/api/v1/employees", nil).Expect(t, 200)
	s.Do("POST", "/api/v1/employees", map[string]any{"firstName": "A", "lastName": "B"}).Expect(t, 201)

	// Cross-site requests with the cookie are refused.
	s.Do("POST", "/api/v1/employees", map[string]any{"firstName": "A", "lastName": "B"}, "Origin", "https://evil.example").Expect(t, 403)

	// A second session, listed and revoked from the first.
	s2 := env.SignIn("owner@test", "")
	list := s.Do("GET", "/api/v1/auth/sessions", nil).Expect(t, 200)
	if n := len(list.Get("data").([]any)); n != 2 {
		t.Fatalf("sessions: %s", list.Raw)
	}
	var other string
	for _, x := range list.Get("data").([]any) {
		if m := x.(map[string]any); m["current"] == false {
			other = m["id"].(string)
		}
	}
	s.Do("DELETE", "/api/v1/auth/sessions/"+other, nil).Expect(t, 204)
	s2.Do("GET", "/api/v1/auth/session", nil).Expect(t, 401)

	// Changing the password signs out other sessions.
	s3 := env.SignIn("owner@test", "")
	s.Do("POST", "/api/v1/auth/password", map[string]any{"currentPassword": "wrong", "newPassword": "a much better password"}).Expect(t, 422)
	s.Do("POST", "/api/v1/auth/password", map[string]any{"currentPassword": testutil.DefaultPassword, "newPassword": "short"}).Expect(t, 422)
	s.Do("POST", "/api/v1/auth/password", map[string]any{"currentPassword": testutil.DefaultPassword, "newPassword": "a much better password"}).Expect(t, 204)
	s3.Do("GET", "/api/v1/auth/session", nil).Expect(t, 401)
	s.Do("GET", "/api/v1/auth/session", nil).Expect(t, 200)

	// Idle sessions expire.
	env.App.Now = func() time.Time { return time.Now().Add(13 * time.Hour) }
	s.Do("GET", "/api/v1/auth/session", nil).Expect(t, 401)
	env.App.Now = time.Now

	// Sign out.
	s4 := env.SignIn("owner@test", "a much better password")
	s4.Do("POST", "/api/v1/auth/sign-out", nil).Expect(t, 204)
	s4.Do("GET", "/api/v1/auth/session", nil).Expect(t, 401)

	// Repeated failures lock the account.
	env.CreateUser(testutil.UserSpec{Email: "lock@test"})
	for range 10 {
		anon.Do("POST", "/api/v1/auth/sign-in", map[string]any{"email": "lock@test", "password": "wrong password!"}).Expect(t, 401)
	}
	r := anon.Do("POST", "/api/v1/auth/sign-in", map[string]any{"email": "lock@test", "password": testutil.DefaultPassword}).Expect(t, 401)
	if !regexp.MustCompile(`Too many failed attempts`).Match(r.Raw) {
		t.Fatalf("expected lockout: %s", r.Raw)
	}
}

func TestInvitationsAndLinks(t *testing.T) {
	env := testutil.New(t, everyScope, nil)
	env.OwnerID()
	owner := env.SignIn("owner@test", "")
	emp := env.Do("POST", "/api/v1/employees", map[string]any{"firstName": "Sam", "lastName": "Lee", "homeLocationId": env.LocationID}).Expect(t, 201).Str("id")

	u := owner.Do("POST", "/api/v1/users", map[string]any{"email": "sam@example.com", "name": "Sam Lee", "employeeId": emp,
		"locationIds": []string{env.LocationID}}).Expect(t, 201)
	if u.Str("status") != "invited" || u.Get("invitationUrl") != nil || u.Str("employeeId") != emp || u.Str("locationIds.0") != env.LocationID {
		t.Fatalf("invite: %s", u.Raw)
	}
	owner.Do("POST", "/api/v1/users", map[string]any{"email": "SAM@example.com", "name": "Again"}).Expect(t, 409)
	token := lastToken(t, env, "invitation")

	anon := env.NewSession()
	anon.Do("POST", "/api/v1/auth/invitations:accept", map[string]any{"token": token, "password": "123"}).Expect(t, 422)
	anon.Do("POST", "/api/v1/auth/invitations:accept", map[string]any{"token": token, "password": "password123"}).Expect(t, 422)
	sam := env.NewSession()
	sam.Do("POST", "/api/v1/auth/invitations:accept", map[string]any{"token": token, "password": "sam's long password"}).Expect(t, 200)
	sam.Do("GET", "/api/v1/me/profile", nil).Expect(t, 200)
	anon.Do("POST", "/api/v1/auth/invitations:accept", map[string]any{"token": token, "password": "sam's long password"}).Expect(t, 422)

	// Magic link.
	anon.Do("POST", "/api/v1/auth/magic-link", map[string]any{"email": "nobody@test"}).Expect(t, 202)
	anon.Do("POST", "/api/v1/auth/magic-link", map[string]any{"email": "sam@example.com"}).Expect(t, 202)
	link := lastToken(t, env, "magic_link")
	for _, e := range env.Emails() {
		if e["to"] == "nobody@test" {
			t.Fatal("email sent to an unknown address")
		}
	}
	ml := env.NewSession()
	ml.Do("POST", "/api/v1/auth/magic-link:redeem", map[string]any{"token": link}).Expect(t, 200)
	ml.Do("GET", "/api/v1/auth/session", nil).Expect(t, 200)
	anon.Do("POST", "/api/v1/auth/magic-link:redeem", map[string]any{"token": link}).Expect(t, 422)

	// Magic links can be turned off.
	owner.Do("PATCH", "/api/v1/settings/authentication", map[string]any{"magicLinkEnabled": false}).Expect(t, 200)
	anon.Do("POST", "/api/v1/auth/magic-link", map[string]any{"email": "sam@example.com"}).Expect(t, 403)

	// Password reset signs out every session.
	anon.Do("POST", "/api/v1/auth/password-reset", map[string]any{"email": "sam@example.com"}).Expect(t, 202)
	reset := lastToken(t, env, "password_reset")
	rs := env.NewSession()
	rs.Do("POST", "/api/v1/auth/password-reset:complete", map[string]any{"token": reset, "password": "a new password for sam"}).Expect(t, 200)
	sam.Do("GET", "/api/v1/auth/session", nil).Expect(t, 401)
	env.SignIn("sam@example.com", "a new password for sam")

	// Invitation links can be returned instead of emailed.
	r := owner.Do("POST", "/api/v1/users", map[string]any{"email": "kim@example.com", "name": "Kim", "sendEmail": false}).Expect(t, 201)
	if r.Str("invitationUrl") == "" {
		t.Fatalf("expected an invitation URL: %s", r.Raw)
	}

	// The CLI can issue a link to recover access.
	cliLink, err := cli.SignInLink(context.Background(), env.Pool, "http://test", "sam@example.com", true)
	if err != nil || !regexp.MustCompile(`/reset-password\?token=`).MatchString(cliLink) {
		t.Fatalf("cli link %q: %v", cliLink, err)
	}
}

func TestTwoFactor(t *testing.T) {
	env := testutil.New(t, everyScope, nil)
	env.OwnerID()
	env.CreateUser(testutil.UserSpec{Email: "ana@test"})
	s := env.SignIn("ana@test", "")

	setup := s.Do("POST", "/api/v1/auth/mfa/totp:setup", nil).Expect(t, 200)
	secret := setup.Str("secret")
	if !regexp.MustCompile(`^otpauth://totp/`).MatchString(setup.Str("otpauthUrl")) {
		t.Fatalf("setup: %s", setup.Raw)
	}
	s.Do("POST", "/api/v1/auth/mfa/totp:confirm", map[string]any{"code": "000000"}).Expect(t, 422)
	step := auth.TOTPStep(time.Now())
	code, _ := auth.TOTPCode(secret, step)
	conf := s.Do("POST", "/api/v1/auth/mfa/totp:confirm", map[string]any{"code": code}).Expect(t, 200)
	codes := conf.Get("recoveryCodes").([]any)
	if len(codes) != 10 {
		t.Fatalf("recovery codes: %s", conf.Raw)
	}

	// Signing in now needs a second step.
	p := env.NewSession()
	r := p.Do("POST", "/api/v1/auth/sign-in", map[string]any{"email": "ana@test", "password": testutil.DefaultPassword}).Expect(t, 200)
	if r.Get("mfaRequired") != true {
		t.Fatalf("expected mfaRequired: %s", r.Raw)
	}
	if p.Do("GET", "/api/v1/me/profile", nil).Expect(t, 401).Str("code") != "mfa_required" {
		t.Fatal("pending session should be limited")
	}
	p.Do("GET", "/api/v1/auth/session", nil).Expect(t, 200)
	// The code used to confirm can't be replayed.
	p.Do("POST", "/api/v1/auth/sign-in/mfa", map[string]any{"code": code}).Expect(t, 422)
	p.Do("POST", "/api/v1/auth/sign-in/mfa", map[string]any{"code": codes[0]}).Expect(t, 200)
	if p.Do("GET", "/api/v1/auth/session", nil).Expect(t, 200).Get("mfa.pending") != false {
		t.Fatal("still pending")
	}

	// A recovery code works once; the next time step's TOTP code works.
	q := env.NewSession()
	q.Do("POST", "/api/v1/auth/sign-in", map[string]any{"email": "ana@test", "password": testutil.DefaultPassword}).Expect(t, 200)
	q.Do("POST", "/api/v1/auth/sign-in/mfa", map[string]any{"code": codes[0]}).Expect(t, 422)
	next, _ := auth.TOTPCode(secret, step+1)
	env.App.Now = func() time.Time { return time.Now().Add(30 * time.Second) }
	q.Do("POST", "/api/v1/auth/sign-in/mfa", map[string]any{"code": next}).Expect(t, 200)
	env.App.Now = time.Now

	// Company-wide 2FA: others must enrol before doing anything else.
	env.OwnerID()
	owner := env.SignIn("owner@test", "")
	owner.Do("PATCH", "/api/v1/settings/authentication", map[string]any{"mfaRequired": true}).Expect(t, 200)
	env.CreateUser(testutil.UserSpec{Email: "bo@test"})
	bo := env.SignIn("bo@test", "")
	if bo.Do("GET", "/api/v1/locations", nil).Expect(t, 403).Str("code") != "mfa_enrollment_required" {
		t.Fatal("expected enrolment requirement")
	}
	if bo.Do("GET", "/api/v1/auth/session", nil).Expect(t, 200).Get("mfa.enrollmentRequired") != true {
		t.Fatal("session should say enrolment is required")
	}
	bo.Do("POST", "/api/v1/auth/mfa/totp:setup", nil).Expect(t, 200)
	// Ana can't turn 2FA off while the company requires it.
	p.Do("POST", "/api/v1/auth/mfa/totp:disable", map[string]any{"password": testutil.DefaultPassword, "code": codes[1]}).Expect(t, 409)
}

func TestRolesAndReach(t *testing.T) {
	env := testutil.New(t, everyScope, nil)
	ctx := context.Background()
	locB, err := cli.CreateLocation(ctx, env.Pool, cli.LocationInput{Name: "Store 102", ExternalID: "102", Timezone: "America/Chicago", Currency: "USD", Cutoff: "04:00"})
	if err != nil {
		t.Fatal(err)
	}
	e1 := env.Do("POST", "/api/v1/employees", map[string]any{"firstName": "At", "lastName": "A", "homeLocationId": env.LocationID}).Expect(t, 201).Str("id")
	e2 := env.Do("POST", "/api/v1/employees", map[string]any{"firstName": "At", "lastName": "B", "homeLocationId": locB}).Expect(t, 201).Str("id")

	env.CreateUser(testutil.UserSpec{Email: "mgr@test", LocationIDs: []string{env.LocationID}, Perms: map[string]string{
		"employees.read": "assigned_locations", "work_orders.create": "assigned_locations", "equipment.read": "assigned_locations",
		"api_keys.personal": "everyone"}})
	m := env.SignIn("mgr@test", "")

	if m.Do("GET", "/api/v1/employees", nil).Expect(t, 403).Str("code") != "out_of_reach" {
		t.Fatal("unfiltered list should be out of reach")
	}
	list := m.Do("GET", "/api/v1/employees?locationId="+env.LocationID, nil).Expect(t, 200)
	if n := len(list.Get("data").([]any)); n != 1 {
		t.Fatalf("list: %s", list.Raw)
	}
	m.Do("GET", "/api/v1/employees?locationId="+locB, nil).Expect(t, 403)
	m.Do("GET", "/api/v1/employees/"+e1, nil).Expect(t, 200)
	m.Do("GET", "/api/v1/employees/"+e2, nil).Expect(t, 403)
	m.Do("POST", "/api/v1/employees", map[string]any{"firstName": "X", "lastName": "Y"}).Expect(t, 403)
	m.Do("GET", "/api/v1/kpis", nil).Expect(t, 403)
	m.Do("GET", "/api/v1/locations", nil).Expect(t, 200) // shared reference data

	// CRUD records are checked by their location.
	wo := m.Do("POST", "/api/v1/work-orders", map[string]any{"title": "Leak", "locationId": env.LocationID}).Expect(t, 201).Str("id")
	m.Do("POST", "/api/v1/work-orders", map[string]any{"title": "Leak", "locationId": locB}).Expect(t, 403)
	other := env.Do("POST", "/api/v1/work-orders", map[string]any{"title": "Door", "locationId": locB}).Expect(t, 201).Str("id")
	m.Do("GET", "/api/v1/work-orders/"+wo, nil).Expect(t, 200)
	m.Do("GET", "/api/v1/work-orders/"+other, nil).Expect(t, 403)

	// Integration-only endpoints stay closed to people.
	m.Do("POST", "/api/v1/time/punches:batch", map[string]any{"source": "x", "punches": []any{}}).Expect(t, 403)

	// Personal keys act with the user's role and reach.
	key := m.Do("POST", "/api/v1/auth/api-keys", map[string]any{"name": "script"}).Expect(t, 201).Str("key")
	env.DoWithKey(key, "GET", "/api/v1/employees/"+e1, nil).Expect(t, 200)
	env.DoWithKey(key, "GET", "/api/v1/employees/"+e2, nil).Expect(t, 403)
	env.DoWithKey(key, "GET", "/api/v1/auth/api-keys", nil).Expect(t, 403) // keys are managed from a session

	// Role administration and escalation rules.
	env.OwnerID()
	owner := env.SignIn("owner@test", "")
	env.CreateUser(testutil.UserSpec{Email: "hr@test", Perms: map[string]string{
		"users.manage": "everyone", "roles.manage": "everyone", "employees.read": "everyone"}})
	hr := env.SignIn("hr@test", "")
	role := hr.Do("POST", "/api/v1/roles", map[string]any{"name": "Viewer", "permissions": []any{
		map[string]any{"permission": "employees.read", "reach": "assigned_locations"}}}).Expect(t, 201)
	hr.Do("POST", "/api/v1/roles", map[string]any{"name": "Payroll", "permissions": []any{
		map[string]any{"permission": "pay.read", "reach": "everyone"}}}).Expect(t, 422)
	hr.Do("POST", "/api/v1/roles", map[string]any{"name": "Admins", "permissions": []any{
		map[string]any{"permission": "roles.manage", "reach": "everyone"}}}).Expect(t, 422)
	hr.Do("POST", "/api/v1/roles", map[string]any{"name": "Bad", "permissions": []any{
		map[string]any{"permission": "no.such", "reach": "everyone"}}}).Expect(t, 422)
	payroll := owner.Do("POST", "/api/v1/roles", map[string]any{"name": "Payroll", "permissions": []any{
		map[string]any{"permission": "pay.read", "reach": "everyone"}}}).Expect(t, 201).Str("id")
	hr.Do("POST", "/api/v1/users", map[string]any{"email": "p@example.com", "name": "P", "roleId": payroll}).Expect(t, 422)
	v := hr.Do("POST", "/api/v1/users", map[string]any{"email": "v@example.com", "name": "V", "roleId": role.Str("id"), "sendEmail": false}).Expect(t, 201)
	hr.Do("PATCH", "/api/v1/users/"+v.Str("id"), map[string]any{"locationIds": []string{locB}}).Expect(t, 200)
	hr.Do("DELETE", "/api/v1/roles/"+role.Str("id"), nil).Expect(t, 409)

	// Deactivating signs the user out; the last Owner is protected.
	var mgrID string
	_ = env.Pool.QueryRow(ctx, `SELECT id FROM users WHERE email = 'mgr@test'`).Scan(&mgrID)
	hr.Do("POST", "/api/v1/users/"+mgrID+":deactivate", nil).Expect(t, 200)
	m.Do("GET", "/api/v1/auth/session", nil).Expect(t, 401)
	env.DoWithKey(key, "GET", "/api/v1/employees/"+e1, nil).Expect(t, 401)
	var ownerID string
	_ = env.Pool.QueryRow(ctx, `SELECT id FROM users WHERE email = 'owner@test'`).Scan(&ownerID)
	hr.Do("POST", "/api/v1/users/"+ownerID+":deactivate", nil).Expect(t, 403)
	owner.Do("POST", "/api/v1/users/"+ownerID+":deactivate", nil).Expect(t, 403)

	// Integration keys can't manage accounts.
	env.Do("POST", "/api/v1/roles", map[string]any{"name": "X", "permissions": []any{}}).Expect(t, 403)
	if n := len(env.Do("GET", "/api/v1/roles", nil).Expect(t, 200).Get("data").([]any)); n < 4 {
		t.Fatalf("roles: %d", n)
	}
}

func TestEmployeeArea(t *testing.T) {
	env := testutil.New(t, everyScope, []string{"punch.corrected"})
	boss := env.Do("POST", "/api/v1/employees", map[string]any{"firstName": "Boss", "lastName": "Lady", "homeLocationId": env.LocationID}).Expect(t, 201).Str("id")
	emp := env.Do("POST", "/api/v1/employees", map[string]any{"firstName": "Dana", "lastName": "Reyes", "homeLocationId": env.LocationID,
		"managerId": boss}).Expect(t, 201).Str("id")
	other := env.Do("POST", "/api/v1/employees", map[string]any{"firstName": "Other", "lastName": "Person"}).Expect(t, 201).Str("id")
	env.CreateUser(testutil.UserSpec{Email: "dana@test", EmployeeID: emp})
	env.CreateUser(testutil.UserSpec{Email: "boss@test", EmployeeID: boss, Perms: map[string]string{"punches.correct": "own_team"}})
	env.CreateUser(testutil.UserSpec{Email: "nobody@test"})
	d := env.SignIn("dana@test", "")

	p := d.Do("GET", "/api/v1/me/profile", nil).Expect(t, 200)
	if p.Str("firstName") != "Dana" || p.Str("manager.name") != "Boss Lady" || p.Str("location.name") != "Store 101" {
		t.Fatalf("profile: %s", p.Raw)
	}
	p = d.Do("PATCH", "/api/v1/me/profile", map[string]any{"phone": "+1 555 0100", "emergencyContacts": []any{
		map[string]any{"name": "Chris", "relationship": "partner", "phone": "+1 555 0101"}}}).Expect(t, 200)
	if p.Str("phone") != "+1 555 0100" || p.Str("emergencyContacts.0.name") != "Chris" {
		t.Fatalf("patch: %s", p.Raw)
	}
	if d.Do("GET", "/api/v1/me/activity", nil).Expect(t, 200).Str("data.0.action") != "employee.self_update" {
		t.Fatal("activity")
	}
	env.SignIn("nobody@test", "").Do("GET", "/api/v1/me/profile", nil).Expect(t, 403)

	// Only my punches.
	env.Do("POST", "/api/v1/time/punches:batch", map[string]any{"source": "clock", "punches": []any{
		map[string]any{"employeeId": emp, "type": "in", "at": "2026-09-20T14:00:00Z"},
		map[string]any{"employeeId": emp, "type": "out", "at": "2026-09-20T22:00:00Z"},
		map[string]any{"employeeId": other, "type": "in", "at": "2026-09-20T14:00:00Z"},
	}}).Expect(t, 202)
	punches := d.Do("GET", "/api/v1/me/punches", nil).Expect(t, 200)
	if n := len(punches.Get("data").([]any)); n != 2 {
		t.Fatalf("punches: %s", punches.Raw)
	}
	outPunch := punches.Str("data.1.id")

	// Punch correction: the manager of the employee's team approves it.
	corr := d.Do("POST", "/api/v1/me/punch-corrections", map[string]any{"punchId": outPunch, "type": "out",
		"at": "2026-09-20T22:30:00Z", "reason": "Forgot to clock out after closing"}).Expect(t, 201)
	d.Do("POST", "/api/v1/punch-corrections/"+corr.Str("id")+":approve", nil).Expect(t, 403)
	b := env.SignIn("boss@test", "")
	approved := b.Do("POST", "/api/v1/punch-corrections/"+corr.Str("id")+":approve", map[string]any{"note": "ok"}).Expect(t, 200)
	if approved.Str("status") != "approved" || approved.Str("createdPunchId") == "" {
		t.Fatalf("approve: %s", approved.Raw)
	}
	b.Do("POST", "/api/v1/punch-corrections/"+corr.Str("id")+":approve", nil).Expect(t, 409)
	after := d.Do("GET", "/api/v1/me/punches", nil).Expect(t, 200)
	if n := len(after.Get("data").([]any)); n != 2 {
		t.Fatalf("voided punch should be hidden: %s", after.Raw)
	}
	if n := len(d.Do("GET", "/api/v1/me/punches?includeVoided=true", nil).Expect(t, 200).Get("data").([]any)); n != 3 {
		t.Fatal("voided punch should be kept")
	}
	// The boss's reach is their own team only.
	oc := env.Pool
	var otherCorr string
	if err := oc.QueryRow(context.Background(), `INSERT INTO punch_corrections (id, employee_id, type, at, reason)
		VALUES ('pcr_x', $1, 'in', now(), 'x') RETURNING id`, other).Scan(&otherCorr); err != nil {
		t.Fatal(err)
	}
	b.Do("POST", "/api/v1/punch-corrections/"+otherCorr+":approve", nil).Expect(t, 403)

	// Time off.
	typ := env.Do("POST", "/api/v1/time-off/types", map[string]any{"name": "Vacation"}).Expect(t, 201).Str("id")
	req := d.Do("POST", "/api/v1/me/time-off/requests", map[string]any{"typeId": typ, "startDate": "2026-10-05", "endDate": "2026-10-06",
		"hours": "16", "employeeId": other}).Expect(t, 201)
	if req.Str("employeeId") != emp {
		t.Fatalf("time off must be for me: %s", req.Raw)
	}
	if n := len(d.Do("GET", "/api/v1/me/time-off/requests", nil).Expect(t, 200).Get("data").([]any)); n != 1 {
		t.Fatal("my requests")
	}
	d.Do("POST", "/api/v1/me/time-off/requests/"+req.Str("id")+":cancel", nil).Expect(t, 200)

	// Pay.
	env.Do("POST", "/api/v1/employees/"+emp+"/pay-rates", map[string]any{"payType": "hourly", "rate": "21.00", "effectiveFrom": "2026-01-01"}).Expect(t, 201)
	pay := d.Do("GET", "/api/v1/me/pay", nil).Expect(t, 200)
	if pay.Str("rates.0.rate") != "21" {
		t.Fatalf("pay: %s", pay.Raw)
	}
	if _, err := env.App.Features.Disable(context.Background(), "employee_area.estimated_pay", "test"); err != nil {
		t.Fatal(err)
	}
	d.Do("GET", "/api/v1/me/pay", nil).Expect(t, 404)

	// Announcements.
	ann := env.Do("POST", "/api/v1/announcements", map[string]any{"title": "Policy", "body": "Read me", "requireAck": true}).Expect(t, 201).Str("id")
	if d.Do("GET", "/api/v1/me/announcements", nil).Expect(t, 200).Str("data.0.id") != ann {
		t.Fatal("announcements")
	}
	d.Do("POST", "/api/v1/me/announcements/"+ann+":acknowledge", nil).Expect(t, 204)
	if d.Do("GET", "/api/v1/me/announcements", nil).Expect(t, 200).Get("data.0.acknowledgedAt") == nil {
		t.Fatal("acknowledged")
	}

	// Export my data.
	zr := d.Do("GET", "/api/v1/me/export", nil).Expect(t, 200)
	if zr.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("export: %s", zr.Header)
	}
	z, err := zip.NewReader(bytes.NewReader(zr.Raw), int64(len(zr.Raw)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range z.File {
		names = append(names, f.Name)
	}
	for _, want := range []string{"profile.json", "punches.json", "punches.csv", "time-off-requests.json", "history.json"} {
		if !slices.Contains(names, want) {
			t.Fatalf("export missing %s: %v", want, names)
		}
	}

	// The whole area can be switched off.
	if _, err := env.App.Features.Disable(context.Background(), "employee_area", "test"); err != nil {
		t.Fatal(err)
	}
	d.Do("GET", "/api/v1/me/profile", nil).Expect(t, 404)

	env.ProcessWebhooks()
	if got := env.Hooks.Types(); !slices.Equal(got, []string{"punch.corrected"}) {
		t.Fatalf("events: %v", got)
	}
}
