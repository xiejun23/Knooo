//go:build !windows

package studio

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
)

func protectModelKey(data string, key []byte, binding []byte, decrypt bool) ([]byte, error) {
	path := filepath.Join(data, "model-keystore")
	master, err := os.ReadFile(path)
	if !decrypt && errors.Is(err, os.ErrNotExist) {
		if err := createModelKeystore(path); err != nil {
			return nil, err
		}
		master, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	if len(master) != 32 {
		return nil, errors.New("invalid model keystore")
	}
	block, err := aes.NewCipher(master)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, err
	}
	if decrypt {
		return gcm.Open(nil, nil, key, binding)
	}
	return gcm.Seal(nil, nil, key, binding), nil
}

func createModelKeystore(path string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".model-keystore-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	master := make([]byte, 32)
	_, _ = rand.Read(master)
	_, err = tmp.Write(master)
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err = errors.Join(err, closeErr); err != nil {
		return err
	}
	if err = os.Link(tmp.Name(), path); errors.Is(err, os.ErrExist) {
		return nil
	}
	return err
}
