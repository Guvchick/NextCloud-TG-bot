package main

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"
)

func createDatabaseBackup(cfg Config, db *DB) (string, error) {
	users, err := db.ListUsers("", 100000, 0)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(cfg.BackupDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(cfg.BackupDir, "bot-"+time.Now().UTC().Format("20060102-150405")+".postgres.json.gz")
	out, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	payload := map[string]any{"generated_at": time.Now().UTC().Format(time.RFC3339), "storage": "postgres", "users": users}
	encoder := json.NewEncoder(gz)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(payload); err != nil {
		_ = gz.Close()
		return "", err
	}
	if err := gz.Close(); err != nil {
		return "", err
	}
	return path, nil
}

func createPublicJSONBackup(cfg Config, db *DB) (string, error) {
	users, err := db.ListUsers("", 100000, 0)
	if err != nil {
		return "", err
	}
	for i := range users {
		users[i].NCPassword = nil
	}
	if err := os.MkdirAll(cfg.BackupDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(cfg.BackupDir, "users-public-"+time.Now().UTC().Format("20060102-150405")+".json.gz")
	out, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	payload := map[string]any{"generated_at": time.Now().UTC().Format(time.RFC3339), "users": users}
	encoder := json.NewEncoder(gz)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(payload); err != nil {
		_ = gz.Close()
		return "", err
	}
	if err := gz.Close(); err != nil {
		return "", err
	}
	return path, nil
}

func restoreDatabaseBackup(backupPath string, db *DB) error {
	if _, err := os.Stat(backupPath); err != nil {
		return err
	}
	in, err := os.Open(backupPath)
	if err != nil {
		return err
	}
	defer in.Close()
	gz, err := gzip.NewReader(in)
	if err != nil {
		return err
	}
	defer gz.Close()
	var payload struct {
		Users []User `json:"users"`
	}
	if err := json.NewDecoder(gz).Decode(&payload); err != nil {
		return err
	}
	return db.RestoreUsers(payload.Users)
}

func listBackups(cfg Config) []string {
	files, _ := filepath.Glob(filepath.Join(cfg.BackupDir, "*.postgres.json.gz"))
	sortByModTime(files)
	if len(files) > 10 {
		files = files[:10]
	}
	return files
}

func pruneBackups(cfg Config) error {
	files, _ := filepath.Glob(filepath.Join(cfg.BackupDir, "*.postgres.json.gz"))
	cutoff := time.Now().Add(-time.Duration(cfg.BackupRetentionDays) * 24 * time.Hour)
	for _, file := range files {
		info, err := os.Stat(file)
		if err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(file)
		}
	}
	return nil
}

func sortByModTime(files []string) {
	for i := 0; i < len(files); i++ {
		for j := i + 1; j < len(files); j++ {
			ii, _ := os.Stat(files[i])
			jj, _ := os.Stat(files[j])
			if ii != nil && jj != nil && jj.ModTime().After(ii.ModTime()) {
				files[i], files[j] = files[j], files[i]
			}
		}
	}
}
