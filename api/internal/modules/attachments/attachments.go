// Package attachments stores uploaded files (proof, photos, receipts,
// documents…) and serves them to the people allowed to see them.
//
// Files are streamed to storage (local disk or S3) with their size limited and
// their SHA-256 recorded. The returned URL can be put in any field that takes
// a link, such as a corrective action's evidenceUrl or a work order's photos.
package attachments

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/selectdev/purros/api/internal/storage"
)

const tag = "Attachments"

// Purposes an attachment can have.
var Purposes = []string{"proof", "photo", "receipt", "invoice", "document", "payslip", "signature", "other"}

type Uploader struct {
	Type string  `json:"type" doc:"user, integration or api_key"`
	ID   *string `json:"id"`
	Name *string `json:"name"`
}

type Attachment struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	ContentType string    `json:"contentType"`
	SizeBytes   int64     `json:"sizeBytes"`
	SHA256      string    `json:"sha256"`
	Purpose     string    `json:"purpose"`
	Note        *string   `json:"note"`
	EmployeeID  *string   `json:"employeeId" doc:"The employee it concerns; they can always see it"`
	LocationID  *string   `json:"locationId"`
	UploadedBy  Uploader  `json:"uploadedBy"`
	URL         string    `json:"url" doc:"Download link (needs the same access); use it in fields that take a link"`
	CreatedAt   time.Time `json:"createdAt"`

	storageKey string
}

type uploadQuery struct {
	Name       string `json:"name,omitempty" doc:"File name (multipart uploads use the part's file name)"`
	Purpose    string `json:"purpose,omitempty" doc:"proof, photo, receipt, invoice, document, payslip, signature or other"`
	EmployeeID string `json:"employeeId,omitempty" doc:"The employee it concerns"`
	LocationID string `json:"locationId,omitempty"`
	Note       string `json:"note,omitempty"`
}

type listQuery struct {
	httpx.ListParams
	EmployeeID string `json:"employeeId,omitempty"`
	LocationID string `json:"locationId,omitempty"`
	Purpose    string `json:"purpose,omitempty"`
}

const cols = `id, name, content_type, size_bytes, sha256, purpose, note, employee_id, location_id,
	uploaded_by_type, uploaded_by_id, uploaded_by_name, created_at, storage_key`

func scan(c *httpx.Ctx, r pgx.Row) (Attachment, error) {
	var a Attachment
	err := r.Scan(&a.ID, &a.Name, &a.ContentType, &a.SizeBytes, &a.SHA256, &a.Purpose, &a.Note, &a.EmployeeID, &a.LocationID,
		&a.UploadedBy.Type, &a.UploadedBy.ID, &a.UploadedBy.Name, &a.CreatedAt, &a.storageKey)
	a.URL = c.App.Config.URL + httpx.APIPrefix + "/attachments/" + a.ID + "/content"
	return a, err
}

func get(c *httpx.Ctx, q db.Querier, id string) (Attachment, error) {
	a, err := scan(c, q.QueryRow(c, `SELECT `+cols+` FROM attachments WHERE id = $1 AND deleted_at IS NULL`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return a, httpx.NotFound("Attachment not found.")
	}
	return a, err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// uploadedByCaller reports whether the caller uploaded a.
func uploadedByCaller(c *httpx.Ctx, a Attachment) bool {
	act := c.Actor()
	return act.ID != "" && a.UploadedBy.Type == act.Type && deref(a.UploadedBy.ID) == act.ID
}

// canRead: integrations with the scope (checked by the route); people who
// uploaded it, whom it concerns, or whose attachments.read reaches it.
func canRead(c *httpx.Ctx, a Attachment) error {
	u := c.Principal.User
	if u == nil || uploadedByCaller(c, a) || (u.EmployeeID != "" && deref(a.EmployeeID) == u.EmployeeID) {
		return nil
	}
	if err := c.CheckPermissionReach("attachments.read", deref(a.LocationID), deref(a.EmployeeID)); err != nil {
		return httpx.NotFound("Attachment not found.") // don't reveal files people can't see
	}
	return nil
}

func canDelete(c *httpx.Ctx, a Attachment) error {
	if c.Principal.User == nil || uploadedByCaller(c, a) {
		return nil
	}
	if err := canRead(c, a); err != nil {
		return err
	}
	return c.CheckPermissionReach("attachments.manage", deref(a.LocationID), deref(a.EmployeeID))
}

// checkTags stops people from attaching files to employees or locations
// outside their reach (which would show the file to others).
func checkTags(c *httpx.Ctx, q db.Querier, employeeID, locationID string) error {
	u := c.Principal.User
	if u == nil {
		return nil
	}
	if employeeID != "" && employeeID != u.EmployeeID {
		if err := c.CheckPermissionReach("attachments.read", "", employeeID); err != nil {
			return httpx.Validation(httpx.FieldError{Path: "employeeId", Message: "You can only attach files to yourself or employees within your reach"})
		}
	}
	if locationID != "" {
		var home *string
		if u.EmployeeID != "" {
			_ = q.QueryRow(c, `SELECT home_location_id FROM employees WHERE id = $1`, u.EmployeeID).Scan(&home)
		}
		if deref(home) != locationID {
			if err := c.CheckPermissionReach("attachments.read", locationID, ""); err != nil {
				return httpx.Validation(httpx.FieldError{Path: "locationId", Message: "You can only attach files to your location or locations within your reach"})
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// File types
// ---------------------------------------------------------------------------

// Types recognised from their content.
var sniffable = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true, "application/pdf": true,
	"video/mp4": true, "video/webm": true, "audio/mpeg": true, "audio/wave": true,
}

// Types recognised by extension when the content isn't distinctive.
var byExtension = map[string]string{
	".heic": "image/heic", ".heif": "image/heif", ".mov": "video/quicktime", ".m4a": "audio/mp4",
	".csv": "text/csv", ".txt": "text/plain",
	".doc": "application/msword", ".xls": "application/vnd.ms-excel",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".odt":  "application/vnd.oasis.opendocument.text", ".ods": "application/vnd.oasis.opendocument.spreadsheet",
}

// Allowed describes the accepted file types for messages.
const Allowed = "images (JPEG, PNG, GIF, WebP, HEIC), PDF, text and CSV, Word, Excel, PowerPoint, OpenDocument, and MP4/MOV video or audio"

// detectType decides the stored content type from the file's first bytes and
// its name. Anything else, including HTML and SVG, is refused.
func detectType(head []byte, name string) (string, bool) {
	sniffed, _, _ := strings.Cut(http.DetectContentType(head), ";")
	ext := strings.ToLower(path.Ext(name))
	switch {
	case sniffable[sniffed]:
		return sniffed, true
	case sniffed == "text/plain":
		// Only plain text and CSV: markup (SVG, HTML, XML) and scripts often
		// sniff as text too.
		switch ext {
		case ".csv":
			return "text/csv", true
		case ".txt", ".log", "":
			return "text/plain", true
		}
	case sniffed == "application/octet-stream" || sniffed == "application/zip":
		t, ok := byExtension[ext]
		if ok && t != "text/csv" && t != "text/plain" {
			return t, true
		}
	}
	return "", false
}

// inline types are shown in the browser; everything else downloads.
func inline(contentType string) bool {
	return strings.HasPrefix(contentType, "image/") || strings.HasPrefix(contentType, "video/") ||
		strings.HasPrefix(contentType, "audio/") || contentType == "application/pdf" || contentType == "text/plain"
}

// cleanName keeps a readable, harmless file name.
func cleanName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.TrimSpace(path.Base(name))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." || name == "/" {
		name = "upload"
	}
	for len(name) > 200 {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	return name
}

// ---------------------------------------------------------------------------
// Upload
// ---------------------------------------------------------------------------

type counter struct{ n int64 }

func (w *counter) Write(p []byte) (int, error) { w.n += int64(len(p)); return len(p), nil }

type stored struct {
	key, contentType, sha string
	size                  int64
}

// store streams one file to storage.
func store(c *httpx.Ctx, r io.Reader, name string, max int64) (stored, error) {
	br := bufio.NewReaderSize(r, 4096)
	head, _ := br.Peek(512)
	if len(head) == 0 {
		return stored{}, httpx.Validation(httpx.FieldError{Path: "file", Message: "The file is empty"})
	}
	contentType, ok := detectType(head, name)
	if !ok {
		return stored{}, httpx.Validation(httpx.FieldError{Path: "file", Message: "This type of file isn't accepted. Allowed: " + Allowed})
	}
	now := c.App.Now().UTC()
	st := stored{key: fmt.Sprintf("attachments/%04d/%02d/%s", now.Year(), now.Month(), ids.New(ids.Attachment)), contentType: contentType}
	h, n := sha256.New(), &counter{}
	body := io.TeeReader(io.LimitReader(br, max+1), io.MultiWriter(h, n))
	if err := c.App.Storage.Put(c, st.key, body, -1, contentType); err != nil {
		return st, fmt.Errorf("store upload: %w", err)
	}
	if n.n > max {
		_ = c.App.Storage.Delete(context.WithoutCancel(c), st.key)
		return st, httpx.TooLarge(fmt.Sprintf("Files can be at most %d MB.", max>>20))
	}
	st.size, st.sha = n.n, hex.EncodeToString(h.Sum(nil))
	return st, nil
}

func upload(c *httpx.Ctx) (any, error) {
	max := int64(c.App.Config.Storage.MaxUploadMB) << 20
	if max <= 0 {
		max = 25 << 20
	}
	meta := uploadQuery{Name: c.Query("name"), Purpose: c.Query("purpose"), EmployeeID: c.Query("employeeId"),
		LocationID: c.Query("locationId"), Note: c.Query("note")}
	var st stored
	have := false
	cleanup := func() {
		if have {
			_ = c.App.Storage.Delete(context.WithoutCancel(c), st.key)
		}
	}

	mediaType, params, _ := mime.ParseMediaType(c.Req.Header.Get("Content-Type"))
	if mediaType == "multipart/form-data" {
		mr := multipart.NewReader(c.Req.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				cleanup()
				return nil, httpx.BadRequest("Malformed multipart body: " + err.Error())
			}
			if part.FileName() != "" || part.FormName() == "file" {
				if have {
					cleanup()
					return nil, httpx.BadRequest("Send one file per request.")
				}
				if meta.Name == "" {
					meta.Name = part.FileName()
				}
				if st, err = store(c, part, meta.Name, max); err != nil {
					return nil, err
				}
				have = true
				continue
			}
			v, _ := io.ReadAll(io.LimitReader(part, 2048))
			val := strings.TrimSpace(string(v))
			switch part.FormName() {
			case "name":
				if !have || meta.Name == "" {
					meta.Name = val
				}
			case "purpose":
				meta.Purpose = val
			case "employeeId":
				meta.EmployeeID = val
			case "locationId":
				meta.LocationID = val
			case "note":
				meta.Note = val
			}
		}
		if !have {
			return nil, httpx.Validation(httpx.FieldError{Path: "file", Message: "Add the file as a form field named \"file\""})
		}
	} else {
		var err error
		if st, err = store(c, c.Req.Body, meta.Name, max); err != nil {
			return nil, err
		}
		have = true
	}

	if meta.Purpose == "" {
		meta.Purpose = "other"
	}
	valid := false
	for _, p := range Purposes {
		valid = valid || p == meta.Purpose
	}
	var out Attachment
	err := c.InTx(func(tx pgx.Tx) error {
		if !valid {
			return httpx.Validation(httpx.FieldError{Path: "purpose", Message: "Must be one of: " + strings.Join(Purposes, ", ")})
		}
		if len(meta.Note) > 1000 {
			return httpx.Validation(httpx.FieldError{Path: "note", Message: "At most 1000 characters"})
		}
		if err := checkTags(c, tx, meta.EmployeeID, meta.LocationID); err != nil {
			return err
		}
		act := c.Actor()
		var err error
		out, err = scan(c, tx.QueryRow(c, `INSERT INTO attachments (id, name, content_type, size_bytes, sha256, storage_key, purpose, note,
			employee_id, location_id, uploaded_by_type, uploaded_by_id, uploaded_by_name)
			VALUES ($1,$2,$3,$4,$5,$6,$7,nullif($8,''),nullif($9,''),nullif($10,''),$11,nullif($12,''),nullif($13,'')) RETURNING `+cols,
			ids.New(ids.Attachment), cleanName(meta.Name), st.contentType, st.size, st.sha, st.key, meta.Purpose, meta.Note,
			meta.EmployeeID, meta.LocationID, act.Type, act.ID, act.Name))
		if db.IsForeignKeyViolation(err) {
			return httpx.Validation(httpx.FieldError{Path: "employeeId", Message: "Unknown employee or location"})
		}
		if err != nil {
			return err
		}
		return c.Record(tx, httpx.Change{Action: "attachment.upload", EventType: "attachment.uploaded", Feature: "core",
			EntityType: "attachment", EntityID: out.ID, LocationID: deref(out.LocationID), After: out})
	})
	if err != nil {
		cleanup()
		return nil, err
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Routes
// ---------------------------------------------------------------------------

func listWhere(c *httpx.Ctx, where string, args ...any) (httpx.Page[Attachment], error) {
	lp, err := c.ParseList()
	if err != nil {
		return httpx.Page[Attachment]{}, err
	}
	args = append(args, lp.AfterID, lp.Limit+1)
	n := len(args)
	rows, err := c.App.Pool.Query(c, fmt.Sprintf(`SELECT `+cols+` FROM attachments WHERE deleted_at IS NULL AND (%s) AND id > $%d
		ORDER BY id LIMIT $%d`, where, n-1, n), args...)
	if err != nil {
		return httpx.Page[Attachment]{}, err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Attachment, error) { return scan(c, r) })
	if err != nil {
		return httpx.Page[Attachment]{}, err
	}
	return httpx.NewPage(items, lp.Limit, func(a Attachment) string { return a.ID }), nil
}

// Routes returns the attachment routes.
func Routes() []httpx.Route {
	return []httpx.Route{
		{
			Method: "POST", Path: "/attachments", Tag: tag, Scope: "attachments:write", Permission: httpx.PermSelf, Stream: true,
			Summary: "Upload a file",
			Description: "Send multipart/form-data with the file in a field named \"file\" (and optional fields name, purpose, employeeId, " +
				"locationId, note), or send the file itself as the body with the same values as query parameters. Accepted: " + Allowed +
				". The size limit is STORAGE_MAX_UPLOAD_MB (25 MB by default).",
			Query: uploadQuery{}, Response: Attachment{}, Status: 201,
			Handler: upload,
		},
		{
			Method: "GET", Path: "/attachments", Tag: tag, Scope: "attachments:read", Permission: "attachments.read",
			Summary: "List attachments", Query: listQuery{}, Response: httpx.Page[Attachment]{},
			Handler: func(c *httpx.Ctx) (any, error) {
				return listWhere(c, `($1 = '' OR employee_id = $1) AND ($2 = '' OR location_id = $2) AND ($3 = '' OR purpose = $3)`,
					c.Query("employeeId"), c.Query("locationId"), c.Query("purpose"))
			},
		},
		{
			Method: "GET", Path: "/me/attachments", Tag: tag, Auth: httpx.AuthUser, Permission: httpx.PermSelf,
			Summary: "Files you uploaded or that concern you", Query: httpx.ListParams{}, Response: httpx.Page[Attachment]{},
			Handler: func(c *httpx.Ctx) (any, error) {
				u := c.Principal.User
				return listWhere(c, `(uploaded_by_type = 'user' AND uploaded_by_id = $1) OR ($2 <> '' AND employee_id = $2)`, u.UserID, u.EmployeeID)
			},
		},
		{
			Method: "GET", Path: "/attachments/{id}", Tag: tag, Scope: "attachments:read", Permission: httpx.PermSelf,
			Summary: "Get an attachment's details", Response: Attachment{},
			Handler: func(c *httpx.Ctx) (any, error) {
				a, err := get(c, c.App.Pool, c.Param("id"))
				if err == nil {
					err = canRead(c, a)
				}
				return a, err
			},
		},
		{
			Method: "GET", Path: "/attachments/{id}/content", Tag: tag, Scope: "attachments:read", Permission: httpx.PermSelf,
			Summary:     "Download an attachment",
			Description: "With S3 storage, answers 302 with a short-lived signed URL; otherwise streams the file.",
			Handler: func(c *httpx.Ctx) (any, error) {
				a, err := get(c, c.App.Pool, c.Param("id"))
				if err == nil {
					err = canRead(c, a)
				}
				if err != nil {
					return nil, err
				}
				ttl := time.Duration(c.App.Config.Storage.SignedURLTTL) * time.Second
				if ttl <= 0 {
					ttl = 5 * time.Minute
				}
				if u, err := c.App.Storage.SignedURL(c, a.storageKey, ttl, a.Name, a.ContentType, inline(a.ContentType)); err == nil {
					return httpx.Redirect{URL: u}, nil
				} else if !errors.Is(err, storage.ErrNoSignedURLs) {
					return nil, err
				}
				body, obj, err := c.App.Storage.Get(c, a.storageKey)
				if errors.Is(err, storage.ErrNotFound) {
					return nil, httpx.NotFound("The file is missing from storage.")
				}
				if err != nil {
					return nil, err
				}
				return httpx.Stream{ContentType: a.ContentType, Filename: a.Name, Inline: inline(a.ContentType), Size: obj.Size, Body: body}, nil
			},
		},
		{
			Method: "DELETE", Path: "/attachments/{id}", Tag: tag, Scope: "attachments:write", Permission: httpx.PermSelf,
			Summary:     "Delete an attachment",
			Description: "Uploaders can delete their own files; others need attachments.manage. The file is removed from storage; its record is kept for the audit log.",
			Status:      204,
			Handler: func(c *httpx.Ctx) (any, error) {
				var key string
				err := c.InTx(func(tx pgx.Tx) error {
					a, err := get(c, tx, c.Param("id"))
					if err != nil {
						return err
					}
					if err := canDelete(c, a); err != nil {
						return err
					}
					key = a.storageKey
					if _, err := tx.Exec(c, `UPDATE attachments SET deleted_at = now() WHERE id = $1`, a.ID); err != nil {
						return err
					}
					return c.Record(tx, httpx.Change{Action: "attachment.delete", EventType: "attachment.deleted", Feature: "core",
						EntityType: "attachment", EntityID: a.ID, LocationID: deref(a.LocationID), Before: a})
				})
				if err != nil {
					return nil, err
				}
				if err := c.App.Storage.Delete(c, key); err != nil {
					c.App.Log.Warn("delete attachment file", "key", key, "err", err)
				}
				return httpx.Result{Status: 204}, nil
			},
		},
	}
}
