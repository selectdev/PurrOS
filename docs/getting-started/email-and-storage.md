# Email (SMTP) & file storage (S3)

PurrOS sends email through any **SMTP** server, and stores files either on local disk or in any **S3-compatible** object storage. Both are set with environment variables (see [Configuration](configuration.md)) and can be tested from **Settings → System** or the [CLI](../operations/cli.md).

---

## Email (SMTP)

### What PurrOS sends by email

| Email | Sent to |
|---|---|
| Invitations, magic sign-in links, password resets | The person signing in |
| Security notices (new device, 2FA changed, account locked) | The account owner |
| Schedule published or changed, shift reminders | Employees (if they chose email) |
| Approval requests and decisions (timesheets, time off, swaps, purchase orders) | Managers and employees |
| Alerts (overdue checklists, cash over/short, low stock, failed integrations) | Chosen roles |
| Scheduled reports (PDF, CSV or Excel attached) | Report recipients |
| Purchase orders (PDF + CSV) | Suppliers, when sending orders by email |
| Customer invoices (PDF) | Customers |
| Announcements | Employees who chose email |

Everyone chooses which notifications they get by email in their profile. Security emails and sign-in links are always sent.

### Settings

| Variable | Required | Default | Description |
|---|---|---|---|
| `SMTP_HOST` | Yes | | SMTP server hostname |
| `SMTP_PORT` | No | `587` | `587` (STARTTLS), `465` (implicit TLS) or `25` |
| `SMTP_SECURE` | No | `false` | `true` for implicit TLS (port 465). On 587, STARTTLS is used automatically. |
| `SMTP_USER`, `SMTP_PASSWORD` | Usually | | Credentials. Many providers use an API key as the password. |
| `SMTP_FROM` | Yes | | Sender, e.g. `Acme Operations <ops@acme.example>` |
| `SMTP_REPLY_TO` | No | | Where replies go, e.g. an HR or support mailbox |
| `SMTP_REQUIRE_TLS` | No | `true` | Refuse to send if the server doesn't support TLS |
| `SMTP_TLS_REJECT_UNAUTHORIZED` | No | `true` | Set `false` only for internal servers with self-signed certificates |
| `SMTP_RATE_PER_SECOND` | No | `10` | Stay under your provider's sending limit |

Emails are sent by the worker from a queue, so a slow or unavailable SMTP server never slows the app down. Failed sends are retried with growing delays for up to 24 hours. Once sent, the email body is deleted; only the recipient, subject, kind and status are kept.

### Example `.env`

```dotenv
SMTP_HOST=smtp.your-provider.example
SMTP_PORT=587
SMTP_USER=apikey
SMTP_PASSWORD=your-smtp-password-or-api-key
SMTP_FROM="Acme Operations <ops@acme.example>"
SMTP_REPLY_TO=hr@acme.example
```

Any SMTP service works: a transactional email provider, Microsoft 365 or Google Workspace SMTP relay, Amazon SES, or your own mail server.

### Make sure email gets delivered

To keep PurrOS email out of spam folders, set these DNS records for the domain in `SMTP_FROM`, following your email provider's instructions:

- **SPF**: authorizes your provider to send for your domain.
- **DKIM**: signs messages so receivers can verify them.
- **DMARC**: tells receivers what to do with unauthenticated mail.

### Test it

- **Settings → System → Email → Send test email**, or
- `docker compose exec api purros email test --to you@example.com`

Both show the exact SMTP error if sending fails.

### Delivery log

**Settings → System → Email** lists recent emails with their status (queued, sent, failed, bounced if your provider reports it) and the reason for failures. Email content is not stored in the log, only the recipient, subject and type.

### Branding and language

Emails use the company logo and name from **Settings → Company**, and are sent in each recipient's language. Admins can edit the wording of invitation and announcement emails under **Settings → Email templates**.

### Without SMTP

PurrOS works without email, but:

- invitation and sign-in links have to be copied from the admin UI and shared by hand
- magic-link sign-in is unavailable
- notifications are in-app and push only, and scheduled reports and emailed POs are disabled

---

## File storage (S3)

### What PurrOS stores as files

| Files | Examples |
|---|---|
| Employee documents | Contracts, IDs, certificates |
| Photos | Kiosk clock-in photos, checklist and audit photos, repair ticket photos, waste photos |
| Receipts and invoices | Paid-out receipts, supplier invoices, generated customer invoices |
| Payslips | PDFs sent in by a payroll integration |
| Shared library | Manuals, policies, training material |
| Imports and exports | Uploaded CSVs, report exports, "Export my data" ZIPs, full company exports |
| Branding | Logo and team display images |

The database only keeps each file's metadata (name, type, size, checksum, owner, who can see it). The file itself lives in storage.

### Drivers

| `STORAGE_DRIVER` | Where files go | Good for |
|---|---|---|
| `local` (default) | A Docker volume inside the server | Single-server installs, evaluation |
| `s3` | Any S3-compatible object storage | Production, multiple app servers, large volumes, easier backups |

S3-compatible services include AWS S3, Cloudflare R2, Backblaze B2, Wasabi, DigitalOcean Spaces, Google Cloud Storage (interoperability mode), and self-hosted MinIO, Garage or Ceph.

### S3 settings

| Variable | Required | Default | Description |
|---|---|---|---|
| `STORAGE_DRIVER` | | `local` | Set to `s3` |
| `STORAGE_S3_BUCKET` | Yes | | Bucket name |
| `STORAGE_S3_REGION` | Yes | | Region, e.g. `eu-central-1` (`auto` for some providers) |
| `STORAGE_S3_ENDPOINT` | Non-AWS | | e.g. `https://s3.eu-central-003.backblazeb2.com` or `http://minio:9000` |
| `STORAGE_S3_ACCESS_KEY_ID`, `STORAGE_S3_SECRET_ACCESS_KEY` | Usually | | Credentials. Leave empty on AWS to use an instance or task IAM role. |
| `STORAGE_S3_FORCE_PATH_STYLE` | No | `false` | `true` for MinIO and some other self-hosted services |
| `STORAGE_S3_PREFIX` | No | | Folder prefix inside the bucket, e.g. `purros/`, to share a bucket |
| `STORAGE_S3_SSE` | No | | Server-side encryption: `AES256` or `aws:kms` |
| `STORAGE_S3_KMS_KEY_ID` | No | | KMS key when using `aws:kms` |
| `STORAGE_SIGNED_URL_TTL` | No | `300` | Seconds a download link stays valid |
| `STORAGE_MAX_UPLOAD_MB` | No | `25` | Largest single upload |

### Example: AWS S3

```dotenv
STORAGE_DRIVER=s3
STORAGE_S3_BUCKET=acme-purros-files
STORAGE_S3_REGION=eu-central-1
STORAGE_S3_ACCESS_KEY_ID=AKIA...
STORAGE_S3_SECRET_ACCESS_KEY=...
STORAGE_S3_SSE=AES256
```

### Example: self-hosted MinIO next to PurrOS

Add to `docker-compose.yml`:

```yaml
  minio:
    image: minio/minio
    command: server /data --console-address ":9001"
    environment:
      MINIO_ROOT_USER: purros
      MINIO_ROOT_PASSWORD: change-me-long-password
    volumes:
      - minio-data:/data
    restart: unless-stopped

volumes:
  minio-data:
```

```dotenv
STORAGE_DRIVER=s3
STORAGE_S3_ENDPOINT=http://minio:9000
STORAGE_S3_REGION=us-east-1
STORAGE_S3_BUCKET=purros
STORAGE_S3_FORCE_PATH_STYLE=true
STORAGE_S3_ACCESS_KEY_ID=purros
STORAGE_S3_SECRET_ACCESS_KEY=change-me-long-password
```

Create the bucket once in the MinIO console (port 9001) or with `purros storage init`.

### Bucket setup

- **Keep the bucket private.** PurrOS never makes files public. Downloads use short-lived **signed URLs** created only after checking that the person may see the file.
- **Turn on versioning**, so deleted or overwritten files can be recovered.
- **Encryption at rest:** use `STORAGE_S3_SSE`, or the provider's default bucket encryption.
- **Lifecycle rules** are optional. PurrOS deletes files itself according to its retention settings (e.g. kiosk photos after 90 days), so don't set rules that delete files PurrOS still references.
- **Minimum permissions** for the access key, on the bucket and prefix only: `s3:GetObject`, `s3:PutObject`, `s3:DeleteObject`, `s3:ListBucket`, `s3:AbortMultipartUpload`.

Example IAM policy for AWS:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    { "Effect": "Allow", "Action": ["s3:ListBucket"], "Resource": "arn:aws:s3:::acme-purros-files" },
    { "Effect": "Allow",
      "Action": ["s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:AbortMultipartUpload"],
      "Resource": "arn:aws:s3:::acme-purros-files/*" }
  ]
}
```

### How uploads and downloads work

- **Uploads** from browsers and phones go straight to the bucket through a signed upload URL, so large photos don't pass through the app server. PurrOS checks the file type and size and records the checksum when the upload finishes.
- **Downloads** go through a permission check, then a redirect to a signed URL that expires after `STORAGE_SIGNED_URL_TTL` seconds.
- If the storage provider doesn't support browser uploads (CORS), set `STORAGE_S3_PROXY_UPLOADS=true` and uploads go through the app instead.

For direct browser uploads, allow CORS on the bucket from your `PURROS_URL`:

```json
[{ "AllowedOrigins": ["https://erp.example.com"], "AllowedMethods": ["PUT", "GET"], "AllowedHeaders": ["*"], "MaxAgeSeconds": 3000 }]
```

### Test it

- **Settings → System → Storage → Test**, or
- `purros storage test`

This writes, reads and deletes a small test file and reports any permission, CORS or endpoint problem.

### Moving from local disk to S3

```bash
# 1. Add the S3 settings to .env, but keep STORAGE_DRIVER=local for now
docker compose exec api purros storage migrate --to s3
# 2. When it reports "0 remaining", switch the driver
#    STORAGE_DRIVER=s3
docker compose up -d
docker compose exec api purros storage verify
```

`storage migrate` copies every file, checks its checksum, and can be stopped and resumed safely. Files uploaded during the migration are copied in a final pass. The local volume isn't deleted, so remove it yourself once you've confirmed everything works.

---

## Database backups to S3 (planned)

Today PurrOS writes its scheduled database backups to a directory (`PURROS_BACKUP_DIR`; see [Backups & upgrades](../operations/backups-and-upgrades.md)). Copy that directory to object storage with your usual tools. Uploading directly to an S3 bucket is planned.

The files in storage are backed up separately, through bucket versioning and replication or your own tools.
