// Package store reads and writes the JSON files the service keeps its state in.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ReadJSON leaves out untouched when the file does not exist.
func ReadJSON(file string, out any) error {
	raw, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(file), err)
	}
	return nil
}

func WriteJSON(file string, payload any) error {
	raw, err := json.MarshalIndent(payload, "", " ")
	if err != nil {
		return err
	}
	return WriteAtomic(file, raw)
}

// WriteAtomic replaces file in one rename, so a reader never sees half of it.
func WriteAtomic(file string, raw []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(file), "."+filepath.Base(file)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), file)
}
