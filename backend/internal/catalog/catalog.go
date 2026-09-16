// Package catalog owns private folder names and published-file listings.
package catalog

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalid  = errors.New("invalid folder or name")
	ErrNotFound = errors.New("folder not found")
	ErrConflict = errors.New("name already exists in folder")
)

type Folder struct {
	ID        string    `json:"id"`
	ParentID  *string   `json:"parent_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type File struct {
	ID          string    `json:"id"`
	OperationID string    `json:"operation_id"`
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	SHA256      []byte    `json:"-"`
	PublishedAt time.Time `json:"published_at"`
}

type Deletion struct {
	FileID      string    `json:"file_id"`
	OperationID string    `json:"operation_id"`
	DeletedAt   time.Time `json:"deleted_at"`
}

type Listing struct {
	Folder  *Folder  `json:"folder"`
	Folders []Folder `json:"folders"`
	Files   []File   `json:"files"`
}

type Service struct {
	Pool  *pgxpool.Pool
	Guard func(context.Context, pgx.Tx) error
}

// LockDirectory serializes namespace changes across files and folders. Callers
// publishing files must use this same lock within the publication transaction.
func LockDirectory(ctx context.Context, tx pgx.Tx, owner string, parent *string) error {
	var id string
	var err error
	if parent == nil {
		err = tx.QueryRow(ctx, `SELECT id::STRING FROM users WHERE id=$1 FOR UPDATE`, owner).Scan(&id)
	} else {
		err = tx.QueryRow(ctx, `SELECT id::STRING FROM folders WHERE id=$1 AND owner_id=$2 FOR UPDATE`, *parent, owner).Scan(&id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func CheckName(ctx context.Context, tx pgx.Tx, owner string, parent *string, name string) error {
	var found bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM folders WHERE owner_id=$1 AND parent_id IS NOT DISTINCT FROM $2::UUID AND name=$3) OR EXISTS(SELECT 1 FROM files WHERE owner_id=$1 AND folder_id IS NOT DISTINCT FROM $2::UUID AND name=$3)`, owner, parent, name).Scan(&found)
	if err != nil {
		return err
	}
	if found {
		return ErrConflict
	}
	return nil
}

func ValidName(name string) bool {
	return name != "" && strings.TrimSpace(name) != "" && name != "." && name != ".." && len(name) <= 255 && utf8.ValidString(name) && !strings.ContainsAny(name, "/\\\x00\r\n")
}

func (s Service) CreateFolder(ctx context.Context, owner string, parent *string, name string) (Folder, error) {
	if !ValidName(name) {
		return Folder{}, ErrInvalid
	}
	var folder Folder
	err := database.WithTx(ctx, s.Pool, func(tx pgx.Tx) error {
		if s.Guard != nil {
			if err := s.Guard(ctx, tx); err != nil {
				return err
			}
		}
		if err := LockDirectory(ctx, tx, owner, parent); err != nil {
			return err
		}
		if err := CheckName(ctx, tx, owner, parent, name); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO folders(owner_id,parent_id,name) VALUES($1,$2,$3) RETURNING id::STRING,parent_id::STRING,name,created_at`, owner, parent, name).Scan(&folder.ID, &folder.ParentID, &folder.Name, &folder.CreatedAt); err != nil {
			return err
		}
		if s.Guard != nil {
			return s.Guard(ctx, tx)
		}
		return nil
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.Code == "23505" {
			return Folder{}, ErrConflict
		}
		if pgErr.Code == "22P02" {
			return Folder{}, ErrInvalid
		}
	}
	return folder, err
}

func (s Service) List(ctx context.Context, owner string, parent *string) (Listing, error) {
	var result Listing
	err := database.WithTx(ctx, s.Pool, func(tx pgx.Tx) error {
		result = Listing{Folders: []Folder{}, Files: []File{}}
		if parent != nil {
			f := Folder{}
			err := tx.QueryRow(ctx, `SELECT id::STRING,parent_id::STRING,name,created_at FROM folders WHERE id=$1 AND owner_id=$2`, *parent, owner).Scan(&f.ID, &f.ParentID, &f.Name, &f.CreatedAt)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			if err != nil {
				return err
			}
			result.Folder = &f
		}
		rows, err := tx.Query(ctx, `SELECT id::STRING,parent_id::STRING,name,created_at FROM folders WHERE owner_id=$1 AND parent_id IS NOT DISTINCT FROM $2::UUID ORDER BY name,id`, owner, parent)
		if err != nil {
			return err
		}
		for rows.Next() {
			f := Folder{}
			if err = rows.Scan(&f.ID, &f.ParentID, &f.Name, &f.CreatedAt); err != nil {
				rows.Close()
				return err
			}
			result.Folders = append(result.Folders, f)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT f.id::STRING,f.operation_id::STRING,f.name,u.size_bytes,u.sha256,f.published_at FROM files f JOIN upload_operations u ON u.id=f.operation_id AND u.owner_id=f.owner_id WHERE f.owner_id=$1 AND f.folder_id IS NOT DISTINCT FROM $2::UUID AND u.status='available' ORDER BY f.name,f.id`, owner, parent)
		if err != nil {
			return err
		}
		for rows.Next() {
			f := File{}
			if err = rows.Scan(&f.ID, &f.OperationID, &f.Name, &f.Size, &f.SHA256, &f.PublishedAt); err != nil {
				rows.Close()
				return err
			}
			result.Files = append(result.Files, f)
		}
		rows.Close()
		return rows.Err()
	})
	return result, err
}

// DeleteFile commits the permanent intent before physical cleanup. Repeating the
// same file ID as its owner returns the original tombstone without incrementing
// publication_generation again.
func (s Service) DeleteFile(ctx context.Context, owner, fileID string) (Deletion, error) {
	var deletion Deletion
	err := database.WithTx(ctx, s.Pool, func(tx pgx.Tx) error {
		guard := func() error {
			if s.Guard != nil {
				return s.Guard(ctx, tx)
			}
			return nil
		}
		if err := guard(); err != nil {
			return err
		}
		loadDeletion := func() error {
			return tx.QueryRow(ctx, `SELECT file_id::STRING,operation_id::STRING,deleted_at
 FROM file_deletions WHERE file_id=$1 AND owner_id=$2`, fileID, owner).Scan(&deletion.FileID, &deletion.OperationID, &deletion.DeletedAt)
		}
		if err := loadDeletion(); err == nil {
			return guard()
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		var parent *string
		if err := tx.QueryRow(ctx, `SELECT folder_id::STRING FROM files WHERE id=$1 AND owner_id=$2`, fileID, owner).Scan(&parent); err != nil {
			return err
		}
		if err := LockDirectory(ctx, tx, owner, parent); err != nil {
			return err
		}
		// A concurrent deletion may have committed while this transaction waited
		// for the directory lock.
		if err := loadDeletion(); err == nil {
			return guard()
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		if err := tx.QueryRow(ctx, `SELECT id::STRING,operation_id::STRING FROM files
 WHERE id=$1 AND owner_id=$2 AND folder_id IS NOT DISTINCT FROM $3::UUID FOR UPDATE`, fileID, owner, parent).Scan(&deletion.FileID, &deletion.OperationID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO file_deletions(file_id,operation_id,owner_id)
 VALUES($1,$2,$3) RETURNING deleted_at`, deletion.FileID, deletion.OperationID, owner).Scan(&deletion.DeletedAt); err != nil {
			return err
		}
		if tag, err := tx.Exec(ctx, `DELETE FROM files WHERE id=$1 AND owner_id=$2`, deletion.FileID, owner); err != nil {
			return err
		} else if tag.RowsAffected() != 1 {
			return pgx.ErrNoRows
		}
		if _, err := tx.Exec(ctx, `UPDATE cluster_configuration
 SET publication_generation=publication_generation+1,updated_at=clock_timestamp() WHERE singleton=true`); err != nil {
			return err
		}
		return guard()
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Deletion{}, ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "22P02" {
		return Deletion{}, ErrInvalid
	}
	return deletion, err
}
