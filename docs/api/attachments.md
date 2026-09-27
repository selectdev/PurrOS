# Attachments (file uploads)

Attachments hold files that go with records: proof for a time-off request or punch correction, photos of waste or a broken machine, delivery receipts, signed forms, payslip PDFs. Each file is stored in [file storage](../getting-started/email-and-storage.md#file-storage-s3) (local disk or an S3 bucket), and PurrOS keeps its details: name, type, size, SHA-256 checksum, who uploaded it, and who it concerns.

An upload returns a `url`. Put that URL in any field that takes a link, for example:
- a corrective action's `evidenceUrl`
- a work order's `photos`
- a waste record's `photoUrl`
- an employee document's `url`
- a payslip's `url`

Opening the URL goes through the same access check as the API.

## Uploading

Send `multipart/form-data` with the file in a field named `file`:

```bash
curl -X POST https://erp.example.com/api/v1/attachments \
  -H "Authorization: Bearer $PURROS_KEY" \
  -F file=@delivery-note.pdf -F purpose=receipt -F locationId=loc_01H…
```

Or send the file itself as the body, with the details as query parameters:

```bash
curl -X POST "https://erp.example.com/api/v1/attachments?name=payslip-2026-09.pdf&purpose=payslip&employeeId=emp_01H…" \
  -H "Authorization: Bearer $PURROS_KEY" -H "Content-Type: application/pdf" --data-binary @payslip.pdf
```

| Field | Description |
|---|---|
| `file` | The file (multipart only) |
| `name` | File name (defaults to the uploaded file's name) |
| `purpose` | `proof`, `photo`, `receipt`, `invoice`, `document`, `payslip`, `signature` or `other` (default) |
| `employeeId` | The employee it concerns. They can always see it. |
| `locationId` | The location it belongs to, for managers' reach |
| `note` | Up to 1,000 characters |

The response (`201`) contains:
- `id` and `url`
- `contentType`, `sizeBytes` and `sha256`
- `purpose`, `employeeId`, `locationId` and `uploadedBy`

### Accepted files

Accepted file types are:
- images: JPEG, PNG, GIF, WebP, HEIC
- PDF
- plain text and CSV
- Word, Excel, PowerPoint and OpenDocument files
- MP4/MOV video, and audio

The type is detected from the file's **content**, not its name. HTML, SVG, scripts and programs are refused (`422`). Files larger than `STORAGE_MAX_UPLOAD_MB` (25 MB by default) are refused with `413 payload_too_large`.

## Downloading

`GET /api/v1/attachments/{id}/content` checks access, then:

- **with S3 storage**, redirects (`302`) to a signed link that expires after `STORAGE_SIGNED_URL_TTL` seconds
- **with local storage**, streams the file

Images, PDFs, video, audio and plain text open in the browser; other files download.

## Who can see a file

| Caller | Can see |
|---|---|
| Integration key | Every attachment, with `attachments:read` |
| The uploader | Their own uploads |
| The employee it concerns (`employeeId`) | Files about them, e.g. payslips or documents HR uploaded |
| Anyone else | Needs `attachments.read`, with a [reach](../admin/users-and-roles.md#reach) covering the file's employee or location. A file with neither needs reach Everyone. |

People can only tag files with their own employee record and home location, or with employees and locations inside their `attachments.read` reach. This stops a file being pushed into someone else's view.

Files a person can't see answer `404`, as if they didn't exist.

## Deleting

`DELETE /api/v1/attachments/{id}` removes the file from storage. Its record is kept for the audit log.

- Uploaders can delete their own files.
- Anyone else needs `attachments.manage` within reach, or an integration key with `attachments:write`.

## Endpoints

| Endpoint | Integration scope | People |
|---|---|---|
| `POST /attachments` | `attachments:write` | anyone signed in |
| `GET /attachments?employeeId=&locationId=&purpose=` | `attachments:read` | `attachments.read` |
| `GET /attachments/{id}` | `attachments:read` | see the access rules above |
| `GET /attachments/{id}/content` | `attachments:read` | see the access rules above |
| `DELETE /attachments/{id}` | `attachments:write` | uploader, or `attachments.manage` |
| `GET /me/attachments` | — | files you uploaded or that concern you |

Webhook events: `attachment.uploaded`, `attachment.deleted`.

## Backups

With local storage, backups include every attachment's file by default (`PURROS_BACKUP_FILES=auto`), and `backup restore` puts them back. With S3 storage, rely on bucket versioning and replication, or set `PURROS_BACKUP_FILES=true` to include the files in backups too. See [Backups & upgrades](../operations/backups-and-upgrades.md).
