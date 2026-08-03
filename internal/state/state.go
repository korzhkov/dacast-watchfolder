package state

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/ysk/dacast-watchfolder/internal/appdir"

	_ "modernc.org/sqlite"
)

type Status string

const (
	StatusQueued    Status = "queued"
	StatusUploading Status = "uploading"
	StatusDone      Status = "done"
	StatusFailed    Status = "failed"
)

type Upload struct {
	Path       string
	Size       int64
	MtimeNs    int64
	Status     Status
	VodID      string
	UploaderID string
	S3Path     string
	PartSize   int64
	Error      string
	UpdatedAt  time.Time
}

type Store struct {
	db *sql.DB
}

func Open() (*Store, error) {
	path, err := appdir.StateDBPath()
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS uploads (
  path TEXT PRIMARY KEY,
  size INTEGER NOT NULL,
  mtime_ns INTEGER NOT NULL,
  status TEXT NOT NULL,
  vod_id TEXT NOT NULL DEFAULT '',
  uploader_id TEXT NOT NULL DEFAULT '',
  s3_path TEXT NOT NULL DEFAULT '',
  part_size INTEGER NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS upload_parts (
  path TEXT NOT NULL,
  part_number INTEGER NOT NULL,
  etag TEXT NOT NULL,
  uploaded_at TEXT NOT NULL,
  PRIMARY KEY (path, part_number)
);
`)
	return err
}

func (s *Store) Get(path string) (*Upload, error) {
	row := s.db.QueryRow(`
SELECT path, size, mtime_ns, status, vod_id, uploader_id, s3_path, part_size, error, updated_at
FROM uploads WHERE path = ?`, path)
	return scanUpload(row)
}

func (s *Store) IsDoneSameIdentity(path string, size, mtimeNs int64) (bool, error) {
	u, err := s.Get(path)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return u.Status == StatusDone && u.Size == size && u.MtimeNs == mtimeNs, nil
}

func (s *Store) UpsertQueued(path string, size, mtimeNs int64) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`
INSERT INTO uploads (path, size, mtime_ns, status, vod_id, uploader_id, s3_path, part_size, error, updated_at)
VALUES (?, ?, ?, ?, '', '', '', 0, '', ?)
ON CONFLICT(path) DO UPDATE SET
  size = excluded.size,
  mtime_ns = excluded.mtime_ns,
  status = CASE
    WHEN uploads.size = excluded.size AND uploads.mtime_ns = excluded.mtime_ns AND uploads.status IN ('done', 'uploading') THEN uploads.status
    ELSE 'queued'
  END,
  vod_id = CASE
    WHEN uploads.status = 'done' AND uploads.size = excluded.size AND uploads.mtime_ns = excluded.mtime_ns THEN uploads.vod_id
    ELSE ''
  END,
  uploader_id = CASE
    WHEN uploads.status IN ('uploading', 'done') AND uploads.size = excluded.size AND uploads.mtime_ns = excluded.mtime_ns THEN uploads.uploader_id
    ELSE ''
  END,
  s3_path = CASE
    WHEN uploads.status IN ('uploading', 'done') AND uploads.size = excluded.size AND uploads.mtime_ns = excluded.mtime_ns THEN uploads.s3_path
    ELSE ''
  END,
  part_size = CASE
    WHEN uploads.status IN ('uploading', 'done') AND uploads.size = excluded.size AND uploads.mtime_ns = excluded.mtime_ns THEN uploads.part_size
    ELSE 0
  END,
  error = CASE
    WHEN uploads.status = 'done' AND uploads.size = excluded.size AND uploads.mtime_ns = excluded.mtime_ns THEN uploads.error
    ELSE ''
  END,
  updated_at = excluded.updated_at
`, path, size, mtimeNs, StatusQueued, now)
	return err
}

func (s *Store) MarkUploading(path string, uploaderID, s3Path string, partSize int64) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`
UPDATE uploads SET status = ?, uploader_id = ?, s3_path = ?, part_size = ?, error = '', updated_at = ?
WHERE path = ?`, StatusUploading, uploaderID, s3Path, partSize, now, path)
	return err
}

func (s *Store) ClearParts(path string) error {
	_, err := s.db.Exec(`DELETE FROM upload_parts WHERE path = ?`, path)
	return err
}

func (s *Store) SavePart(path string, partNumber int, etag string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`
INSERT INTO upload_parts (path, part_number, etag, uploaded_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(path, part_number) DO UPDATE SET etag = excluded.etag, uploaded_at = excluded.uploaded_at
`, path, partNumber, etag, now)
	return err
}

func (s *Store) Parts(path string) (map[int]string, error) {
	rows, err := s.db.Query(`SELECT part_number, etag FROM upload_parts WHERE path = ? ORDER BY part_number`, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int]string)
	for rows.Next() {
		var n int
		var etag string
		if err := rows.Scan(&n, &etag); err != nil {
			return nil, err
		}
		out[n] = etag
	}
	return out, rows.Err()
}

func (s *Store) OrderedETags(path string, totalParts int) ([]string, error) {
	parts, err := s.Parts(path)
	if err != nil {
		return nil, err
	}
	out := make([]string, totalParts)
	for i := 1; i <= totalParts; i++ {
		etag, ok := parts[i]
		if !ok {
			return nil, fmt.Errorf("missing etag for part %d", i)
		}
		out[i-1] = etag
	}
	return out, nil
}

func (s *Store) MarkDone(path string, vodID string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`
UPDATE uploads SET status = ?, vod_id = ?, error = '', updated_at = ? WHERE path = ?`,
		StatusDone, vodID, now, path); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM upload_parts WHERE path = ?`, path); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) MarkFailed(path string, errMsg string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`
UPDATE uploads SET status = ?, error = ?, updated_at = ? WHERE path = ?`,
		StatusFailed, errMsg, now, path)
	return err
}

func (s *Store) ResetSession(path string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`
UPDATE uploads SET status = ?, uploader_id = '', s3_path = '', part_size = 0, error = '', updated_at = ?
WHERE path = ?`, StatusQueued, now, path); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM upload_parts WHERE path = ?`, path); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListNeedingUpload() ([]Upload, error) {
	rows, err := s.db.Query(`
SELECT path, size, mtime_ns, status, vod_id, uploader_id, s3_path, part_size, error, updated_at
FROM uploads
WHERE status IN ('queued', 'uploading', 'failed')
ORDER BY updated_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Upload
	for rows.Next() {
		u, err := scanUploadRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

type scannable interface {
	Scan(dest ...any) error
}

func scanUpload(row scannable) (*Upload, error) {
	var u Upload
	var updated string
	var status string
	err := row.Scan(&u.Path, &u.Size, &u.MtimeNs, &status, &u.VodID, &u.UploaderID, &u.S3Path, &u.PartSize, &u.Error, &updated)
	if err != nil {
		return nil, err
	}
	u.Status = Status(status)
	if t, err := time.Parse(time.RFC3339Nano, updated); err == nil {
		u.UpdatedAt = t
	}
	return &u, nil
}

func scanUploadRows(rows *sql.Rows) (*Upload, error) {
	return scanUpload(rows)
}
