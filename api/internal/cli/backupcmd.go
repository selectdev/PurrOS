package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/selectdev/purros/api/internal/backup"
	"github.com/selectdev/purros/api/internal/config"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/events"
	"github.com/selectdev/purros/api/internal/modules/platform"
	"github.com/selectdev/purros/api/internal/secure"
	"github.com/selectdev/purros/api/internal/storage"
	"github.com/spf13/cobra"
)

// backupDir is where backups go when no --dir is given.
func backupDir(cfg config.Config, flag string) string {
	switch {
	case flag != "":
		return flag
	case cfg.Backup.Dir != "":
		return cfg.Backup.Dir
	}
	return "backups"
}

// passphrase finds the passphrase for a backup: --passphrase-file, then
// PURROS_BACKUP_PASSPHRASE, then a prompt when allowed.
func (a *app) passphrase(cfg config.Config, file string, prompt, confirm bool) (string, error) {
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	if cfg.Backup.Passphrase != "" {
		return cfg.Backup.Passphrase, nil
	}
	if !prompt {
		return "", nil
	}
	if !a.canPrompt() {
		return "", errors.New("a passphrase is needed: use --passphrase-file or PURROS_BACKUP_PASSPHRASE")
	}
	p, err := a.askSecret("Backup passphrase")
	if err != nil {
		return "", err
	}
	if confirm {
		if len(p) < 12 {
			return "", errors.New("use a passphrase of at least 12 characters")
		}
		again, err := a.askSecret("Repeat the passphrase")
		if err != nil {
			return "", err
		}
		if again != p {
			return "", errors.New("the passphrases don't match")
		}
	}
	return p, nil
}

// backupRemote is the S3 bucket backups are uploaded to, or nil.
func backupRemote(cfg config.Config) (storage.Driver, error) {
	if !cfg.Backup.S3Enabled {
		return nil, nil
	}
	return storage.NewS3(cfg.Backup.S3)
}

// resolveBackup accepts a local path or "s3:<name>", downloading the latter
// to a temporary file.
func (a *app) resolveBackup(ctx context.Context, arg string) (string, func(), error) {
	name, remote := strings.CutPrefix(arg, "s3:")
	if !remote {
		return arg, func() {}, nil
	}
	cfg, _ := config.Load(false)
	r, err := backupRemote(cfg)
	if err != nil {
		return "", nil, err
	}
	if r == nil {
		return "", nil, errors.New("s3: backups need PURROS_BACKUP_S3_ENABLED=true and the PURROS_BACKUP_S3_* settings")
	}
	dir, err := os.MkdirTemp("", "purros-restore-")
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(dir, filepath.Base(name))
	if !a.jsonOut {
		a.printf("Downloading %s from %s…\n", name, r.Describe())
	}
	if err := backup.Download(ctx, r, name, path); err != nil {
		os.RemoveAll(dir)
		return "", nil, err
	}
	return path, func() { os.RemoveAll(dir) }, nil
}

func parseAge(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	if d, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(d)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("%q isn't a number of days like 30d", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

type backupJSON struct {
	Path          string    `json:"path"`
	SizeBytes     int64     `json:"sizeBytes"`
	Encrypted     bool      `json:"encrypted"`
	Tables        int       `json:"tables"`
	Rows          int64     `json:"rows"`
	SchemaVersion int64     `json:"schemaVersion"`
	PurrOSVersion string    `json:"purrosVersion"`
	Company       string    `json:"company"`
	CreatedAt     time.Time `json:"createdAt"`
	Files         int       `json:"files"`
	FileBytes     int64     `json:"fileBytes"`
	RemoteKey     string    `json:"remoteKey,omitempty"`
}

func toJSON(path string, size int64, m backup.Manifest) backupJSON {
	return backupJSON{Path: path, SizeBytes: size, Encrypted: m.Encrypted, Tables: len(m.Tables), Rows: m.Rows(),
		SchemaVersion: m.SchemaVersion, PurrOSVersion: m.PurrOSVersion, Company: m.Company, CreatedAt: m.CreatedAt,
		Files: len(m.Files), FileBytes: m.FileBytes()}
}

func (a *app) backupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Create, verify, restore and prune database backups",
		Long: `Complete, consistent database backups made by PurrOS itself (no pg_dump needed),
safe to take while PurrOS is running. Backups can be encrypted with a passphrase.

Scheduled backups: set PURROS_BACKUP_DIR and/or PURROS_BACKUP_S3_ENABLED (with
the PURROS_BACKUP_S3_* bucket settings), and the worker makes one a day and
keeps PURROS_BACKUP_KEEP locally and PURROS_BACKUP_S3_KEEP in the bucket.

Backups in the bucket are named s3:<file>, e.g. purros backup restore s3:purros-….purros-backup.

Restoring needs the same PURROS_SECRET the backup was made with.`,
	}

	var out, dir, passFile string
	var encrypt, noEncrypt, withFiles, noFiles, noUpload bool
	var keep int
	create := &cobra.Command{
		Use:   "create",
		Short: "Back up the database (and uploaded files) now",
		Long: `Back up the database now. Uploaded files are included by default with local
storage (PURROS_BACKUP_FILES). With PURROS_BACKUP_S3_ENABLED the backup is also
uploaded to the backup bucket.`,
		Example: `  purros backup create
  purros backup create --encrypt --dir /mnt/backups --keep 30
  purros backup create --out /tmp/before-migration.purros-backup --no-upload`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			return withPool(ctx, true, func(cfg config.Config, pool *pgxpool.Pool) error {
				pass, err := a.passphrase(cfg, passFile, encrypt, true)
				if err != nil {
					return err
				}
				if noEncrypt {
					pass = ""
				}
				o := backup.RunOptions{Kind: backup.KindManual, Path: out, Dir: dir, Keep: keep,
					Options: backup.Options{Passphrase: pass, PurrOSVersion: platform.Version, SecretFingerprint: secure.Fingerprint(cfg.Secret)}}
				if !noUpload {
					if o.Remote, err = backupRemote(cfg); err != nil {
						return err
					}
					o.RemoteKeep = cfg.Backup.S3Keep
				}
				if o.Dir == "" && o.Path == "" {
					o.Dir = cfg.Backup.Dir
					if o.Dir == "" && o.Remote == nil {
						o.Dir = "backups"
					}
				}
				if (cfg.IncludeFiles() || withFiles) && !noFiles {
					if o.Files, err = storage.New(cfg.Storage); err != nil {
						return err
					}
				}
				start := time.Now()
				res, err := backup.Run(ctx, pool, o)
				var upErr *backup.UploadError
				if err != nil && !errors.As(err, &upErr) {
					return err
				}
				j := toJSON(res.Path, res.Size, res.Manifest)
				j.RemoteKey = res.RemoteKey
				perr := a.emit(j, func() {
					a.printf("✓ Backup created in %s\n", time.Since(start).Round(100*time.Millisecond))
					rows := [][]string{}
					if res.Path != "" {
						rows = append(rows, []string{"  File", res.Path})
					}
					if res.RemoteKey != "" {
						rows = append(rows, []string{"  Uploaded to", o.Remote.Describe() + res.RemoteKey})
					}
					rows = append(rows,
						[]string{"  Size", humanBytes(res.Size)},
						[]string{"  Database", fmt.Sprintf("%d rows in %d tables, schema version %d", res.Manifest.Rows(), len(res.Manifest.Tables), res.Manifest.SchemaVersion)},
						[]string{"  Uploaded files", filesText(res.Manifest, o.Files != nil)},
						[]string{"  Encrypted", map[bool]string{true: "yes", false: "no"}[pass != ""]})
					a.table("", rows)
					if len(res.Manifest.MissingFiles) > 0 {
						a.printf("! %d attachment(s) were missing from storage and aren't in the backup.\n", len(res.Manifest.MissingFiles))
					}
					if pass == "" {
						a.printf("\nThe backup holds personal data and isn't encrypted: store it safely, or use --encrypt.\n")
					}
					if res.RemoteKey == "" {
						a.printf("Copy it off this server. ")
					}
					a.printf("Restoring needs this PURROS_SECRET (fingerprint %s).\n", secure.Fingerprint(cfg.Secret))
				})
				if upErr != nil {
					return upErr
				}
				return perr
			})
		},
	}
	cf := create.Flags()
	cf.StringVar(&out, "out", "", "write to this file")
	cf.StringVar(&dir, "dir", "", "directory for the backup (default $PURROS_BACKUP_DIR, or ./backups without S3)")
	cf.BoolVar(&encrypt, "encrypt", false, "encrypt with a passphrase (prompted, or PURROS_BACKUP_PASSPHRASE / --passphrase-file)")
	cf.BoolVar(&noEncrypt, "no-encrypt", false, "don't encrypt, even when PURROS_BACKUP_PASSPHRASE is set")
	cf.StringVar(&passFile, "passphrase-file", "", "file holding the passphrase")
	cf.IntVar(&keep, "keep", 0, "afterwards, delete all but this many newest backups in the directory")
	cf.BoolVar(&withFiles, "files", false, "include uploaded files (default with local storage)")
	cf.BoolVar(&noFiles, "no-files", false, "leave uploaded files out")
	cf.BoolVar(&noUpload, "no-upload", false, "don't upload to the S3 backup bucket")
	cmd.AddCommand(create)

	var listDir string
	var listRemote bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List backups in the backup directory (or --remote bucket), and recent backup runs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _ := config.Load(false)
			d := backupDir(cfg, listDir)
			var files []backup.File
			var err error
			if listRemote {
				r, err := backupRemote(cfg)
				if err != nil {
					return err
				}
				if r == nil {
					return errors.New("--remote needs PURROS_BACKUP_S3_ENABLED=true and the PURROS_BACKUP_S3_* settings")
				}
				objs, err := backup.RemoteList(cmd.Context(), r)
				if err != nil {
					return err
				}
				d = r.Describe()
				for _, o := range objs {
					files = append(files, backup.File{Path: "s3:" + o.Key, Size: o.Size, ModTime: o.ModTime})
				}
			} else if files, err = backup.List(d); err != nil {
				return err
			}
			var runs []backup.LastRun
			if cfg.DatabaseURL != "" {
				if pool, err := db.ConnectAs(cmd.Context(), cfg.DatabaseURL, "purros-cli"); err == nil {
					runs, _ = backup.Last(cmd.Context(), pool, 10)
					pool.Close()
				}
			}
			return a.emit(map[string]any{"dir": d, "files": files, "runs": runs}, func() {
				if len(files) == 0 {
					a.printf("No backups in %s.\n", d)
				} else {
					var rows [][]string
					for _, f := range files {
						rows = append(rows, []string{filepath.Base(f.Path), humanBytes(f.Size), f.ModTime.Local().Format("2006-01-02 15:04"), ago(f.ModTime)})
					}
					a.printf("Backups in %s:\n", d)
					a.table("FILE\tSIZE\tCREATED\tAGE", rows)
				}
				if len(runs) > 0 {
					var rows [][]string
					for _, r := range runs {
						upload := "-"
						switch {
						case r.RemoteKey != nil:
							upload = "uploaded"
						case r.UploadError != nil:
							upload = "failed: " + *r.UploadError
						}
						file := filepath.Base(deref(r.File))
						if r.File == nil {
							file = deref(r.RemoteKey)
						}
						rows = append(rows, []string{r.StartedAt.Local().Format("2006-01-02 15:04"), r.Kind, r.Status, file, upload, deref(r.Error)})
					}
					a.printf("\nRecent runs:\n")
					a.table("STARTED\tKIND\tSTATUS\tFILE\tS3\tERROR", rows)
				}
			})
		},
	}
	list.Flags().StringVar(&listDir, "dir", "", "backup directory (default $PURROS_BACKUP_DIR or ./backups)")
	list.Flags().BoolVar(&listRemote, "remote", false, "list the backups in the S3 backup bucket")
	cmd.AddCommand(list)

	var downloadOut string
	download := &cobra.Command{
		Use:   "download <name>",
		Short: "Download a backup from the S3 backup bucket",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _ := config.Load(false)
			r, err := backupRemote(cfg)
			if err != nil {
				return err
			}
			if r == nil {
				return errors.New("downloading needs PURROS_BACKUP_S3_ENABLED=true and the PURROS_BACKUP_S3_* settings")
			}
			name := strings.TrimPrefix(args[0], "s3:")
			dst := downloadOut
			if dst == "" {
				dst = filepath.Join(backupDir(cfg, ""), name)
			} else if st, err := os.Stat(dst); err == nil && st.IsDir() {
				dst = filepath.Join(dst, name)
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
				return err
			}
			if err := backup.Download(cmd.Context(), r, name, dst); err != nil {
				return err
			}
			a.printf("Downloaded to %s. Check it with: purros backup verify %s\n", dst, dst)
			return nil
		},
	}
	download.Flags().StringVar(&downloadOut, "out", "", "file or directory to save to (default the backup directory)")
	cmd.AddCommand(download)

	var verifyPass string
	verify := &cobra.Command{
		Use:   "verify <file | s3:name>",
		Short: "Check that a backup is complete and undamaged",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, cleanup, err := a.resolveBackup(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			defer cleanup()
			m, size, err := a.readBackup(path, verifyPass)
			if err != nil {
				return err
			}
			return a.emit(toJSON(args[0], size, m), func() {
				a.printf("✓ %s is complete: %d rows in %d tables, made %s by PurrOS %s (schema %d).\n",
					filepath.Base(args[0]), m.Rows(), len(m.Tables), m.CreatedAt.Local().Format("2006-01-02 15:04"), m.PurrOSVersion, m.SchemaVersion)
			})
		},
	}
	verify.Flags().StringVar(&verifyPass, "passphrase-file", "", "file holding the passphrase")
	cmd.AddCommand(verify)

	var inspectPass string
	inspect := &cobra.Command{
		Use:   "inspect <file | s3:name>",
		Short: "Show what a backup contains",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, cleanup, err := a.resolveBackup(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			defer cleanup()
			m, size, err := a.readBackup(path, inspectPass)
			if err != nil {
				return err
			}
			if a.jsonOut {
				return a.emit(m, nil)
			}
			cfg, _ := config.Load(false)
			secretNote := "unknown"
			if cfg.Secret != "" {
				secretNote = map[bool]string{true: "matches this PURROS_SECRET", false: "DOESN'T match this PURROS_SECRET"}[secure.Fingerprint(cfg.Secret) == m.SecretFingerprint]
			}
			a.table("", [][]string{
				{"File", args[0]}, {"Size", humanBytes(size)}, {"Company", m.Company},
				{"Created", m.CreatedAt.Local().Format("2006-01-02 15:04:05")}, {"PurrOS version", m.PurrOSVersion},
				{"Schema version", fmt.Sprint(m.SchemaVersion)}, {"Encrypted", map[bool]string{true: "yes", false: "no"}[m.Encrypted]},
				{"Secret", m.SecretFingerprint + " (" + secretNote + ")"},
				{"Contents", fmt.Sprintf("%d rows in %d tables", m.Rows(), len(m.Tables))},
				{"Uploaded files", fmt.Sprintf("%d (%s)", len(m.Files), humanBytes(m.FileBytes()))},
			})
			if len(m.MissingFiles) > 0 {
				a.printf("! %d attachment(s) were missing from storage when the backup was made.\n", len(m.MissingFiles))
			}
			tables := append([]backup.Table(nil), m.Tables...)
			sort.Slice(tables, func(i, j int) bool { return tables[i].Rows > tables[j].Rows })
			var rows [][]string
			for _, t := range tables {
				if t.Rows > 0 {
					rows = append(rows, []string{t.Name, fmt.Sprint(t.Rows), humanBytes(t.Bytes)})
				}
			}
			a.printf("\n")
			a.table("TABLE\tROWS\tSIZE", rows)
			return nil
		},
	}
	inspect.Flags().StringVar(&inspectPass, "passphrase-file", "", "file holding the passphrase")
	cmd.AddCommand(inspect)

	var restorePass, safetyDir string
	var replace, noSafety bool
	restore := &cobra.Command{
		Use:   "restore <file | s3:name>",
		Short: "Replace the database with a backup",
		Long: `Replace the database with a backup, then upgrade it to this version's schema.

The backup is fully verified before anything is changed. When the database
already has data, --replace is required and a safety backup of the current
data is taken first (unless --no-safety-backup). Stop PurrOS (the API and
workers) before restoring.`,
		Example: `  docker compose stop api
  docker compose run --rm api backup restore /backups/purros-20260927-020000-scheduled.purros-backup --replace
  docker compose run --rm api backup restore s3:purros-20260927-020000-scheduled.purros-backup --replace
  docker compose start api`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			path, cleanup, err := a.resolveBackup(ctx, args[0])
			if err != nil {
				return err
			}
			defer cleanup()
			return withPool(ctx, true, func(cfg config.Config, pool *pgxpool.Pool) error {
				m, size, err := a.readBackup(path, restorePass)
				if err != nil {
					return err
				}
				pass := a.backupPass
				if m.Encrypted && pass == "" {
					return errors.New("this backup is encrypted: give the passphrase")
				}
				if !a.jsonOut {
					a.printf("Backup: %s, %s, made %s (PurrOS %s, schema %d), %d rows.\n",
						m.Company, humanBytes(size), m.CreatedAt.Local().Format("2006-01-02 15:04"), m.PurrOSVersion, m.SchemaVersion, m.Rows())
				}
				if m.SecretFingerprint != "" && m.SecretFingerprint != secure.Fingerprint(cfg.Secret) {
					a.printf("! This backup was made with a different PURROS_SECRET. Encrypted values (integration\n  settings, webhook secrets, 2FA) won't be readable unless you use that secret.\n")
					if err := a.confirm("Restore anyway?"); err != nil {
						return err
					}
				}
				if latest := db.LatestVersion(); m.SchemaVersion > latest {
					return fmt.Errorf("the backup is from a newer PurrOS (schema %d, this version knows %d): update PurrOS first", m.SchemaVersion, latest)
				}
				if n, err := db.ServerConnections(ctx, pool); err == nil && n > 0 {
					a.printf("! %d connection(s) from a running PurrOS server or worker. Stop them before restoring.\n", n)
					if err := a.confirm("Restore while PurrOS is running?"); err != nil {
						return err
					}
				}
				full, err := backup.HasData(ctx, pool)
				if err != nil {
					return err
				}
				if full {
					if !replace {
						return errors.New("the database already has data: add --replace to overwrite it")
					}
					var company string
					_ = pool.QueryRow(ctx, `SELECT name FROM company LIMIT 1`).Scan(&company)
					if err := a.confirmTyped(fmt.Sprintf("replaces ALL current data of %q", company), company); err != nil {
						return err
					}
					if !noSafety {
						sp := backup.NewPath(backupDir(cfg, safetyDir), time.Now(), backup.KindPreRestore)
						a.printf("Taking a safety backup of the current data…\n")
						so := backup.Options{Passphrase: cfg.Backup.Passphrase, PurrOSVersion: platform.Version, SecretFingerprint: secure.Fingerprint(cfg.Secret)}
						if cfg.IncludeFiles() {
							if so.Files, err = storage.New(cfg.Storage); err != nil {
								return err
							}
						}
						res, err := backup.ToFile(ctx, pool, sp, backup.KindPreRestore, so)
						if err != nil {
							return fmt.Errorf("safety backup failed, nothing was restored: %w", err)
						}
						a.printf("  saved to %s\n", res.Path)
					}
				} else if err := a.confirm("Restore this backup into the database?"); err != nil {
					return err
				}
				var files storage.Driver
				if len(m.Files) > 0 {
					if files, err = storage.New(cfg.Storage); err != nil {
						return err
					}
				}
				start := time.Now()
				_, err = backup.Restore(ctx, pool, path, backup.RestoreOptions{Passphrase: pass, Replace: true, Files: files,
					Progress: func(s string) {
						if !a.jsonOut {
							a.printf("  %s…\n", s)
						}
					}})
				if err != nil {
					return fmt.Errorf("restore failed: %w", err)
				}
				_ = events.Audit(ctx, pool, events.AuditEntry{Actor: cliActor, Action: "backup.restored",
					After: map[string]any{"file": filepath.Base(path), "createdAt": m.CreatedAt, "rows": m.Rows()}})
				return a.emit(toJSON(path, size, m), func() {
					a.printf("✓ Restored in %s. Start PurrOS again, then run `purros doctor`.\n", time.Since(start).Round(100*time.Millisecond))
				})
			})
		},
	}
	rf := restore.Flags()
	rf.StringVar(&restorePass, "passphrase-file", "", "file holding the passphrase")
	rf.BoolVar(&replace, "replace", false, "overwrite a database that already has data")
	rf.BoolVar(&noSafety, "no-safety-backup", false, "don't back up the current data first")
	rf.StringVar(&safetyDir, "safety-dir", "", "where to put the safety backup (default $PURROS_BACKUP_DIR or ./backups)")
	cmd.AddCommand(restore)

	var pruneDir, olderThan string
	var pruneKeep int
	var dryRun, pruneRemote bool
	prune := &cobra.Command{
		Use:   "prune",
		Short: "Delete old backups, always keeping the newest",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _ := config.Load(false)
			d := backupDir(cfg, pruneDir)
			if pruneKeep == 0 && olderThan == "" {
				pruneKeep = cfg.Backup.Keep
			}
			age, err := parseAge(olderThan)
			if err != nil {
				return err
			}
			if pruneRemote {
				r, err := backupRemote(cfg)
				if err != nil {
					return err
				}
				if r == nil {
					return errors.New("--remote needs PURROS_BACKUP_S3_ENABLED=true and the PURROS_BACKUP_S3_* settings")
				}
				keep := cfg.Backup.S3Keep
				if cmd.Flags().Changed("keep") {
					keep = pruneKeep
				}
				if keep <= 0 {
					return errors.New("give --keep")
				}
				if dryRun {
					objs, err := backup.RemoteList(cmd.Context(), r)
					if err != nil {
						return err
					}
					for i, o := range objs {
						if i > 0 && i >= keep {
							a.printf("would delete s3:%s\n", o.Key)
						}
					}
					return nil
				}
				removed, err := backup.RemotePrune(cmd.Context(), r, keep)
				if err != nil {
					return err
				}
				return a.emit(removed, func() { a.printf("Deleted %d backup(s) from %s.\n", len(removed), r.Describe()) })
			}
			if dryRun {
				files, err := backup.List(d)
				if err != nil {
					return err
				}
				for i, f := range files {
					if i > 0 && ((pruneKeep > 0 && i >= pruneKeep) || (age > 0 && time.Since(f.ModTime) > age)) {
						a.printf("would delete %s\n", f.Path)
					}
				}
				return nil
			}
			removed, err := backup.Prune(d, pruneKeep, age, time.Now())
			if err != nil {
				return err
			}
			return a.emit(removed, func() { a.printf("Deleted %d backup(s) from %s.\n", len(removed), d) })
		},
	}
	prune.Flags().StringVar(&pruneDir, "dir", "", "backup directory")
	prune.Flags().IntVar(&pruneKeep, "keep", 0, "keep this many newest backups (default $PURROS_BACKUP_KEEP)")
	prune.Flags().StringVar(&olderThan, "older-than", "", "delete backups older than this, e.g. 30d")
	prune.Flags().BoolVar(&dryRun, "dry-run", false, "only list what would be deleted")
	prune.Flags().BoolVar(&pruneRemote, "remote", false, "prune the S3 backup bucket (keeps --keep, default $PURROS_BACKUP_S3_KEEP)")
	cmd.AddCommand(prune)
	return cmd
}

// readBackup verifies a backup, asking for the passphrase when needed.
func (a *app) readBackup(path, passFile string) (backup.Manifest, int64, error) {
	st, err := os.Stat(path)
	if err != nil {
		return backup.Manifest{}, 0, err
	}
	enc, err := backup.NeedsPassphrase(path)
	if err != nil {
		return backup.Manifest{}, 0, err
	}
	cfg, _ := config.Load(false)
	pass := ""
	if enc {
		if pass, err = a.passphrase(cfg, passFile, true, false); err != nil {
			return backup.Manifest{}, 0, err
		}
		a.backupPass = pass // reused by restore after verification
	}
	m, err := backup.Verify(path, pass)
	return m, st.Size(), err
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h", int(d.Hours()))
	}
	return fmt.Sprintf("%d days", int(d.Hours()/24))
}

func filesText(m backup.Manifest, included bool) string {
	if !included {
		return "not included"
	}
	return fmt.Sprintf("%d (%s)", len(m.Files), humanBytes(m.FileBytes()))
}
