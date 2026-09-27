package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/selectdev/purros/api/internal/config"
	"github.com/selectdev/purros/api/internal/storage"
	"github.com/spf13/cobra"
)

func (a *app) storageCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "storage", Short: "Check file storage (local disk or S3)"}
	var backups bool
	test := &cobra.Command{
		Use:   "test",
		Short: "Write, read and delete a test file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _ := config.Load(false)
			var d storage.Driver
			var err error
			if backups {
				if !cfg.Backup.S3Enabled {
					return errors.New("PURROS_BACKUP_S3_ENABLED isn't set")
				}
				d, err = storage.NewS3(cfg.Backup.S3)
			} else {
				d, err = storage.New(cfg.Storage)
			}
			if err != nil {
				return err
			}
			if err := storageCheck(cmd.Context(), d); err != nil {
				return err
			}
			a.printf("✓ %s works: a test file was written, read back and deleted.\n", d.Describe())
			return nil
		},
	}
	test.Flags().BoolVar(&backups, "backups", false, "test the S3 backup bucket instead")
	cmd.AddCommand(test)

	var initBackups bool
	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Create the S3 bucket if it doesn't exist",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _ := config.Load(false)
			c := cfg.Storage.S3
			if initBackups {
				c = cfg.Backup.S3
			} else if cfg.Storage.Driver != "s3" {
				return errors.New("STORAGE_DRIVER isn't s3 (use --backups for the backup bucket)")
			}
			s, err := storage.NewS3(c)
			if err != nil {
				return err
			}
			created, err := s.EnsureBucket(cmd.Context(), c.Region)
			if err != nil {
				return err
			}
			if created {
				a.printf("Created bucket %s. Keep it private and turn on versioning.\n", c.Bucket)
			} else {
				a.printf("Bucket %s already exists.\n", c.Bucket)
			}
			return nil
		},
	}
	initCmd.Flags().BoolVar(&initBackups, "backups", false, "create the S3 backup bucket instead")
	cmd.AddCommand(initCmd)

	var to string
	migrate := &cobra.Command{
		Use:   "migrate",
		Short: "Copy every uploaded file to another storage (e.g. from local disk to S3)",
		Long: `Copy every uploaded file from the current storage (STORAGE_DRIVER) to the other
one, checking each file's SHA-256. It can be stopped and run again: files
already copied are skipped. Afterwards, switch STORAGE_DRIVER and restart.`,
		Example: `  # .env: keep STORAGE_DRIVER=local, add the STORAGE_S3_* settings
  purros storage migrate --to s3
  # then set STORAGE_DRIVER=s3 and restart PurrOS`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			return withPool(ctx, false, func(cfg config.Config, pool *pgxpool.Pool) error {
				src, err := storage.New(cfg.Storage)
				if err != nil {
					return err
				}
				var dst storage.Driver
				switch to {
				case cfg.Storage.Driver:
					return fmt.Errorf("files are already in %s storage", to)
				case "s3":
					dst, err = storage.NewS3(cfg.Storage.S3)
				case "local":
					dst = storage.NewLocal(cfg.Storage.LocalPath)
				default:
					return errors.New("--to must be s3 or local")
				}
				if err != nil {
					return err
				}
				rows, err := pool.Query(ctx, `SELECT storage_key, size_bytes, sha256, content_type FROM attachments WHERE deleted_at IS NULL ORDER BY id`)
				if err != nil {
					return err
				}
				type att struct {
					key, sum, ct string
					size         int64
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (att, error) {
					var x att
					return x, r.Scan(&x.key, &x.size, &x.sum, &x.ct)
				})
				if err != nil {
					return err
				}
				copied, skipped := 0, 0
				var failed []string
				for _, x := range list {
					if obj, err := dst.Stat(ctx, x.key); err == nil && obj.Size == x.size {
						skipped++
						continue
					}
					body, _, err := src.Get(ctx, x.key)
					if err != nil {
						failed = append(failed, fmt.Sprintf("%s: %v", x.key, err))
						continue
					}
					h := sha256.New()
					err = dst.Put(ctx, x.key, io.TeeReader(body, h), x.size, x.ct)
					body.Close()
					if err == nil && hex.EncodeToString(h.Sum(nil)) != x.sum {
						_ = dst.Delete(ctx, x.key)
						err = errors.New("content doesn't match its checksum")
					}
					if err != nil {
						failed = append(failed, fmt.Sprintf("%s: %v", x.key, err))
						continue
					}
					copied++
				}
				a.printf("Copied %d file(s) to %s, %d already there, %d failed.\n", copied, dst.Describe(), skipped, len(failed))
				for _, f := range failed {
					a.printf("  ✗ %s\n", f)
				}
				if len(failed) > 0 {
					return exitError{code: 1}
				}
				a.printf("Now set STORAGE_DRIVER=%s and restart PurrOS. The old copies weren't deleted.\n", to)
				return nil
			})
		},
	}
	migrate.Flags().StringVar(&to, "to", "", "s3 or local")
	cmd.AddCommand(migrate)

	var checksums bool
	verify := &cobra.Command{
		Use:   "verify",
		Short: "Check that every attachment's file is in storage",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			return withPool(ctx, false, func(cfg config.Config, pool *pgxpool.Pool) error {
				d, err := storage.New(cfg.Storage)
				if err != nil {
					return err
				}
				rows, err := pool.Query(ctx, `SELECT id, storage_key, size_bytes, sha256 FROM attachments WHERE deleted_at IS NULL ORDER BY id`)
				if err != nil {
					return err
				}
				type att struct {
					id, key, sum string
					size         int64
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (att, error) {
					var x att
					return x, r.Scan(&x.id, &x.key, &x.size, &x.sum)
				})
				if err != nil {
					return err
				}
				var problems []string
				for _, x := range list {
					if checksums {
						body, _, err := d.Get(ctx, x.key)
						if err != nil {
							problems = append(problems, fmt.Sprintf("%s: %v", x.id, err))
							continue
						}
						h := sha256.New()
						_, err = io.Copy(h, body)
						body.Close()
						if err != nil || hex.EncodeToString(h.Sum(nil)) != x.sum {
							problems = append(problems, x.id+": content doesn't match its checksum")
						}
						continue
					}
					obj, err := d.Stat(ctx, x.key)
					switch {
					case err != nil:
						problems = append(problems, fmt.Sprintf("%s: %v", x.id, err))
					case obj.Size != x.size:
						problems = append(problems, fmt.Sprintf("%s: size %d, expected %d", x.id, obj.Size, x.size))
					}
				}
				if err := a.emit(map[string]any{"checked": len(list), "problems": problems}, func() {
					for _, p := range problems {
						a.printf("  ✗ %s\n", p)
					}
					a.printf("Checked %d attachment(s) in %s: %d problem(s).\n", len(list), d.Describe(), len(problems))
				}); err != nil {
					return err
				}
				if len(problems) > 0 {
					return exitError{code: 1}
				}
				return nil
			})
		},
	}
	verify.Flags().BoolVar(&checksums, "checksums", false, "read every file and compare its SHA-256 (slower)")
	cmd.AddCommand(verify)
	return cmd
}
