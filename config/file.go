package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/unaquatic/flow/internal/flock"
)

// fileMode is the permission set of a new File, private to its user because
// files such as config.json and auth.json can hold API keys.
const fileMode = 0o600

// File is a JSON object file that processes share and change one top-level
// key at a time, such as config.json and auth.json. It keeps nothing in
// memory, so a change any process makes shows in the next read.
//
// Reads take no lock: a write replaces the file with a rename, so a read
// sees the file as it was before the write or after it, never part of
// either. Writes hold an exclusive lock on the lock file beside it, LockName,
// so concurrent updates apply one after another and none is lost. The lock
// file stays when the write is done. The locks are advisory: programs that
// edit the file without them are not excluded.
type File struct {
	// Dir is the directory that holds the file and its lock file. Update
	// creates it, private to its user, when it is missing.
	Dir string
	// Name is the name of the file in Dir, such as "auth.json".
	Name string
}

// Path returns the path of the file: Name in Dir, or the file it links to
// when it is a symbolic link, which need not exist yet.
func (f File) Path() (string, error) {
	path := filepath.Join(f.Dir, f.Name)
	real, err := filepath.EvalSymlinks(path)
	switch {
	case err == nil:
		return real, nil
	case errors.Is(err, fs.ErrNotExist):
		// No file yet, or a link to a file not created yet.
		if target, err := os.Readlink(path); err == nil {
			if !filepath.IsAbs(target) {
				target = filepath.Join(f.Dir, target)
			}
			return target, nil
		}
		return path, nil
	default:
		return "", err
	}
}

// Read returns the top-level keys of the file and their JSON values. A file
// that is missing or empty has none. Read fails when the file is not a JSON
// object.
func (f File) Read() (map[string]json.RawMessage, error) {
	path, err := f.Path()
	if err != nil {
		return nil, err
	}
	keys, _, _, err := read(path)
	return keys, err
}

// Update applies change to the JSON value of key and saves the result,
// keeping the file's other keys and its permissions, and a symbolic link the
// file is. change gets nil when the file does not set key, and removes key
// by returning nil. A new file is private to its user. Nothing is written
// when change fails or leaves the file as it was, and Update fails rather
// than replace a file that is not a JSON object.
//
// Update holds the lock while change runs, so change must not update the
// same file, which would wait for it.
func (f File) Update(ctx context.Context, key string, change func(json.RawMessage) (json.RawMessage, error)) error {
	return f.Edit(ctx, func(keys map[string]json.RawMessage) error {
		value, err := change(keys[key])
		if err != nil {
			return err
		}
		if value == nil {
			delete(keys, key)
		} else {
			keys[key] = value
		}
		return nil
	})
}

// Edit lets change edit the top-level keys of the file and their JSON
// values in place, and saves the result, as Update does, so that keys that
// belong together change in one write. change gets an empty map when the
// file is missing or empty.
func (f File) Edit(ctx context.Context, change func(keys map[string]json.RawMessage) error) (err error) {
	unlock, err := f.lock(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()

	path, err := f.Path()
	if err != nil {
		return err
	}
	keys, old, mode, err := read(path)
	if err != nil {
		return err
	}
	if keys == nil {
		keys = make(map[string]json.RawMessage)
	}
	if err := change(keys); err != nil {
		return err
	}
	data, err := json.MarshalIndent(keys, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if bytes.Equal(data, old) {
		return nil
	}
	return replace(path, data, mode)
}

// LockName returns the name of the lock file of the file in Dir: Name with
// its extension replaced by ".lock", such as "auth.lock" of "auth.json".
func (f File) LockName() string {
	return strings.TrimSuffix(f.Name, filepath.Ext(f.Name)) + ".lock"
}

// lock takes the exclusive lock of the file, creating Dir and the lock file
// when they are missing, and returns the function that releases it.
func (f File) lock(ctx context.Context) (unlock func() error, err error) {
	if err := os.MkdirAll(f.Dir, 0o700); err != nil {
		return nil, err
	}
	lf, err := os.OpenFile(filepath.Join(f.Dir, f.LockName()), os.O_RDWR|os.O_CREATE, fileMode)
	if err != nil {
		return nil, err
	}
	if err := flock.Lock(ctx, lf, true); err != nil {
		return nil, errors.Join(fmt.Errorf("lock %s: %w", lf.Name(), err), lf.Close())
	}
	return func() error { return errors.Join(flock.Unlock(lf), lf.Close()) }, nil
}

// read returns the keys of the file at path, its contents, and its
// permissions, which are fileMode when it is missing.
func read(path string) (keys map[string]json.RawMessage, data []byte, mode fs.FileMode, err error) {
	data, err = os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil, fileMode, nil
	case err != nil:
		return nil, nil, 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, 0, err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &keys); err != nil {
			return nil, nil, 0, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	return keys, data, info.Mode().Perm(), nil
}

// replace writes data to a new file beside path, with permissions mode, and
// renames it over path, so that a reader sees the old file or the new one,
// never part of either.
func replace(path string, data []byte, mode fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		return errors.Join(err, tmp.Close())
	}
	if err := tmp.Chmod(mode); err != nil {
		return errors.Join(err, tmp.Close())
	}
	// Sync before the rename, so that a crash cannot leave the new name on
	// data not yet written.
	if err := tmp.Sync(); err != nil {
		return errors.Join(err, tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Section is the value of one top-level key of a File, decoded as T. T may
// define its own JSON form, by implementing json.Marshaler and
// json.Unmarshaler.
type Section[T any] struct {
	// File is the file that holds the section.
	File File
	// Key is the top-level key of the section in File.
	Key string
}

// Load returns the value of the section, or the zero T when the file does
// not set it. It fails when the file cannot be read or the value does not
// decode as T.
func (s Section[T]) Load() (T, error) {
	var v T
	keys, err := s.File.Read()
	if err != nil {
		return v, err
	}
	err = s.decode(keys[s.Key], &v)
	return v, err
}

// Update applies change to the value of the section and saves it, as
// File.Update does. change gets the zero T when the file does not set the
// section. A T with an IsZero method that reports true afterwards removes
// the section from the file.
func (s Section[T]) Update(ctx context.Context, change func(*T) error) error {
	return s.File.Update(ctx, s.Key, func(raw json.RawMessage) (json.RawMessage, error) {
		var v T
		if err := s.decode(raw, &v); err != nil {
			return nil, err
		}
		if err := change(&v); err != nil {
			return nil, err
		}
		if z, ok := any(v).(interface{ IsZero() bool }); ok && z.IsZero() {
			return nil, nil
		}
		return json.Marshal(v)
	})
}

// Set saves v as the value of the section, as Update does.
func (s Section[T]) Set(ctx context.Context, v T) error {
	return s.Update(ctx, func(old *T) error {
		*old = v
		return nil
	})
}

// decode decodes raw, the value of the section, into v, leaving v as it is
// when raw is nil.
func (s Section[T]) decode(raw json.RawMessage, v *T) error {
	if raw == nil {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		path, _ := s.File.Path()
		return fmt.Errorf("%s: %s: %w", path, s.Key, err)
	}
	return nil
}
