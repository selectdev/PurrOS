package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/selectdev/purros/api/internal/storage"
	"github.com/selectdev/purros/api/internal/storage/storagetest"
	"github.com/selectdev/purros/api/internal/testutil"
)

var pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 100)...)

type upload struct {
	status int
	body   map[string]any
	raw    string
}

// uploadFile posts a multipart upload with a session cookie or an API key.
func uploadFile(t *testing.T, env *testutil.Env, cookie, key, name string, content []byte, fields map[string]string) upload {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(content)
	mw.Close()
	req, _ := http.NewRequest("POST", env.Server.URL+"/api/v1/attachments", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
		req.Header.Set("Origin", testutil.Origin)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	u := upload{status: resp.StatusCode, raw: string(raw)}
	_ = json.Unmarshal(raw, &u.body)
	return u
}

// download fetches content without following redirects.
func download(t *testing.T, env *testutil.Env, cookie, id string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", env.Server.URL+"/api/v1/attachments/"+id+"/content", nil)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestAttachments(t *testing.T) {
	env := testutil.New(t, []string{"people:write", "attachments:read", "attachments:write"}, []string{"attachment.uploaded"})
	e1 := env.Do("POST", "/api/v1/employees", map[string]any{"firstName": "Ana", "lastName": "A", "homeLocationId": env.LocationID}).Expect(t, 201).Str("id")
	e2 := env.Do("POST", "/api/v1/employees", map[string]any{"firstName": "Bo", "lastName": "B"}).Expect(t, 201).Str("id")
	env.CreateUser(testutil.UserSpec{Email: "ana@test", EmployeeID: e1})
	env.CreateUser(testutil.UserSpec{Email: "bo@test", EmployeeID: e2})
	env.CreateUser(testutil.UserSpec{Email: "mgr@test", LocationIDs: []string{env.LocationID},
		Perms: map[string]string{"attachments.read": "assigned_locations"}})
	ana, bo, mgr := env.SignIn("ana@test", ""), env.SignIn("bo@test", ""), env.SignIn("mgr@test", "")

	// An employee uploads proof for themselves, at their location.
	u := uploadFile(t, env, ana.Cookie, "", "doctor note.png", pngBytes, map[string]string{"purpose": "proof", "employeeId": e1,
		"locationId": env.LocationID, "note": "Sick day 2026-09-20"})
	if u.status != 201 || u.body["contentType"] != "image/png" || u.body["purpose"] != "proof" || u.body["sizeBytes"].(float64) != float64(len(pngBytes)) {
		t.Fatalf("upload: %d %s", u.status, u.raw)
	}
	id := u.body["id"].(string)
	if !strings.HasSuffix(u.body["url"].(string), "/api/v1/attachments/"+id+"/content") || len(u.body["sha256"].(string)) != 64 {
		t.Fatalf("upload fields: %s", u.raw)
	}

	// Refused: HTML, SVG, unknown binaries, too large, empty, tagging someone else.
	for name, content := range map[string][]byte{
		"page.html": []byte("<html><script>alert(1)</script></html>"),
		"logo.svg":  []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`),
		"tool.exe":  append([]byte("MZ"), bytes.Repeat([]byte{0}, 100)...),
		"empty.png": {},
	} {
		if r := uploadFile(t, env, ana.Cookie, "", name, content, nil); r.status != 422 {
			t.Fatalf("%s accepted: %d %s", name, r.status, r.raw)
		}
	}
	big := append(append([]byte(nil), pngBytes...), bytes.Repeat([]byte{1}, 1<<20)...)
	if r := uploadFile(t, env, ana.Cookie, "", "big.png", big, nil); r.status != 413 {
		t.Fatalf("large file: %d %s", r.status, r.raw)
	}
	if r := uploadFile(t, env, ana.Cookie, "", "x.png", pngBytes, map[string]string{"employeeId": e2}); r.status != 422 {
		t.Fatalf("tagging another employee: %d %s", r.status, r.raw)
	}
	if r := uploadFile(t, env, ana.Cookie, "", "x.png", pngBytes, map[string]string{"purpose": "selfie"}); r.status != 422 {
		t.Fatalf("bad purpose: %d", r.status)
	}
	// Office files are recognised by extension; CSV by content and name.
	if r := uploadFile(t, env, ana.Cookie, "", "hours.csv", []byte("day,hours\nmon,8\n"), nil); r.status != 201 || r.body["contentType"] != "text/csv" {
		t.Fatalf("csv: %s", r.raw)
	}

	// Who can see it: the uploader and the employee, and managers whose reach covers the location.
	ana.Do("GET", "/api/v1/attachments/"+id, nil).Expect(t, 200)
	bo.Do("GET", "/api/v1/attachments/"+id, nil).Expect(t, 404)
	mgr.Do("GET", "/api/v1/attachments/"+id, nil).Expect(t, 200)
	mgr.Do("GET", "/api/v1/attachments", nil).Expect(t, 403) // must narrow to a location
	if n := len(mgr.Do("GET", "/api/v1/attachments?locationId="+env.LocationID, nil).Expect(t, 200).Get("data").([]any)); n != 1 {
		t.Fatalf("manager list: %d", n)
	}
	if n := len(ana.Do("GET", "/api/v1/me/attachments", nil).Expect(t, 200).Get("data").([]any)); n != 2 {
		t.Fatalf("my attachments: %d", n)
	}

	// Local storage streams the file.
	resp := download(t, env, ana.Cookie, id)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Equal(body, pngBytes) || resp.Header.Get("Content-Type") != "image/png" ||
		!strings.HasPrefix(resp.Header.Get("Content-Disposition"), `inline; filename="doctor note.png"`) {
		t.Fatalf("download: %d %v", resp.StatusCode, resp.Header)
	}
	if r := download(t, env, bo.Cookie, id); r.StatusCode != 404 {
		t.Fatalf("other employee downloaded: %d", r.StatusCode)
	}

	// Integrations upload raw bodies with query parameters.
	req, _ := http.NewRequest("POST", env.Server.URL+"/api/v1/attachments?name=payslip.pdf&purpose=payslip&employeeId="+e2,
		strings.NewReader("%PDF-1.7\n1 0 obj\n"))
	req.Header.Set("Authorization", "Bearer "+env.Key)
	req.Header.Set("Content-Type", "application/pdf")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var slip map[string]any
	_ = json.NewDecoder(res.Body).Decode(&slip)
	res.Body.Close()
	if res.StatusCode != 201 || slip["contentType"] != "application/pdf" || slip["uploadedBy"].(map[string]any)["type"] != "integration" {
		t.Fatalf("integration upload: %d %v", res.StatusCode, slip)
	}
	bo.Do("GET", "/api/v1/attachments/"+slip["id"].(string), nil).Expect(t, 200) // it concerns Bo
	ana.Do("GET", "/api/v1/attachments/"+slip["id"].(string), nil).Expect(t, 404)

	// Deleting: others need attachments.manage; the uploader can.
	mgr.Do("DELETE", "/api/v1/attachments/"+id, nil).Expect(t, 403)
	ana.Do("DELETE", "/api/v1/attachments/"+id, nil).Expect(t, 204)
	ana.Do("GET", "/api/v1/attachments/"+id, nil).Expect(t, 404)
	if files, _ := env.App.Storage.List(t.Context(), "attachments/"); len(files) != 2 { // csv + payslip remain
		t.Fatalf("stored files: %v", files)
	}

	env.ProcessWebhooks()
	if n := count(env.Hooks.Types(), "attachment.uploaded"); n != 3 {
		t.Fatalf("attachment.uploaded events: %d", n)
	}
}

func TestAttachmentsOnS3(t *testing.T) {
	env := testutil.New(t, []string{"attachments:read", "attachments:write"}, nil)
	s3, err := storage.NewS3(storagetest.S3(t, "files"))
	if err != nil {
		t.Fatal(err)
	}
	env.App.Storage = s3
	u := uploadFile(t, env, "", env.Key, "receipt.jpg", append([]byte("\xff\xd8\xff\xe0"), bytes.Repeat([]byte{7}, 5000)...), map[string]string{"purpose": "receipt"})
	if u.status != 201 || u.body["contentType"] != "image/jpeg" {
		t.Fatalf("upload: %s", u.raw)
	}
	req, _ := http.NewRequest("GET", env.Server.URL+"/api/v1/attachments/"+u.body["id"].(string)+"/content", nil)
	req.Header.Set("Authorization", "Bearer "+env.Key)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if resp.StatusCode != 302 || !strings.Contains(loc, "X-Amz-Signature") {
		t.Fatalf("expected a signed redirect: %d %s", resp.StatusCode, loc)
	}
	got, err := http.Get(loc)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(got.Body)
	got.Body.Close()
	if got.StatusCode != 200 || len(data) != 5004 {
		t.Fatalf("signed download: %d, %d bytes", got.StatusCode, len(data))
	}
}
