package catalog

import (
	"context"
	"database/sql"
)

type ManagedStorageReferences struct {
	ROM   []string
	Media []string
}

// ManagedStorageReferences returns the authoritative live paths below the
// service-owned ROM and media roots. Referenced library/NAS paths are excluded.
func (s *Store) ManagedStorageReferences(ctx context.Context) (ManagedStorageReferences, error) {
	result := ManagedStorageReferences{ROM: []string{}, Media: []string{}}
	return managedStorageReferences(ctx, s.db, result)
}

type storageReferenceQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func managedStorageReferences(ctx context.Context, query storageReferenceQuerier, result ManagedStorageReferences) (ManagedStorageReferences, error) {
	rows, err := query.QueryContext(ctx, `SELECT path FROM artifacts WHERE storage_kind='managed' ORDER BY path`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var value string
		if err = rows.Scan(&value); err != nil {
			rows.Close()
			return result, err
		}
		result.ROM = append(result.ROM, value)
	}
	if err = rows.Close(); err != nil {
		return result, err
	}
	rows, err = query.QueryContext(ctx, `SELECT DISTINCT path FROM media_assets WHERE storage_kind='managed' ORDER BY path`)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var value string
		if err = rows.Scan(&value); err != nil {
			return result, err
		}
		result.Media = append(result.Media, value)
	}
	return result, rows.Err()
}

// WithLockedManagedStorageReferences holds an IMMEDIATE SQLite transaction
// while apply checks and moves managed files. Catalog mutations cannot create a
// new reference between the final mark phase and the filesystem rename.
func (s *Store) WithLockedManagedStorageReferences(ctx context.Context, apply func(ManagedStorageReferences) error) error {
	connection, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer connection.Close()
	if _, err = connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	references, err := managedStorageReferences(ctx, connection, ManagedStorageReferences{ROM: []string{}, Media: []string{}})
	if err != nil {
		return err
	}
	if err = apply(references); err != nil {
		return err
	}
	if _, err = connection.ExecContext(ctx, `COMMIT`); err != nil {
		return err
	}
	committed = true
	return nil
}
