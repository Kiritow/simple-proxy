package certs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kiritow/simple-proxy/internal/nginx"
)

// Dir is a directory holding the files of certificates for nginx to read.
//
// Each version of a certificate has a directory of its own, named after a
// hash of its contents, rather than after the certificate, whose name is
// meant for people. Replacing a certificate therefore changes the paths in
// the nginx configuration: nginx tests the new files, and the old ones stay
// in place if it rejects them.
type Dir string

// Files returns the paths of the files of a certificate, which Write
// creates.
func (d Dir) Files(certPEM, keyPEM []byte) nginx.Certificate {
	h := sha256.New()
	h.Write(certPEM)
	h.Write([]byte{0})
	h.Write(keyPEM)
	dir := filepath.Join(string(d), hex.EncodeToString(h.Sum(nil)[:16]))
	return nginx.Certificate{
		Fullchain: filepath.Join(dir, "fullchain.pem"),
		Key:       filepath.Join(dir, "privkey.pem"),
	}
}

// versionDir matches the directories of certificates, also as named by
// earlier versions after the certificate, so that Prune leaves anything else
// alone.
var versionDir = regexp.MustCompile(`^([0-9a-f]{32}|[A-Za-z0-9_.-]+-[0-9a-f]{16})$`)

const tempPrefix = ".tmp-"

// Write creates the files of a certificate, unless they exist. The private
// key is only readable by its owner, such as root, as nginx reads it.
func (d Dir) Write(certPEM, keyPEM []byte) error {
	files := d.Files(certPEM, keyPEM)
	dir := filepath.Dir(files.Fullchain)
	if _, err := os.Stat(dir); err == nil {
		return nil
	}
	if err := os.MkdirAll(string(d), 0o755); err != nil {
		return err
	}
	// Files appear completely or not at all.
	tmp, err := os.MkdirTemp(string(d), tempPrefix)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, filepath.Base(files.Fullchain)), certPEM, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, filepath.Base(files.Key)), keyPEM, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, dir); err != nil {
		if _, statErr := os.Stat(dir); statErr != nil {
			return err
		}
		// Someone else wrote the same files meanwhile.
	}
	return nil
}

// Prune removes the files of certificates other than those in keep, as
// returned by Files.
func (d Dir) Prune(keep []nginx.Certificate) error {
	entries, err := os.ReadDir(string(d))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	kept := make(map[string]bool, len(keep))
	for _, c := range keep {
		kept[filepath.Base(filepath.Dir(c.Fullchain))] = true
	}
	var errs []error
	for _, e := range entries {
		name := e.Name()
		ours := versionDir.MatchString(name) || strings.HasPrefix(name, tempPrefix)
		if !e.IsDir() || !ours || kept[name] {
			continue
		}
		errs = append(errs, os.RemoveAll(filepath.Join(string(d), name)))
	}
	return errors.Join(errs...)
}
