package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type loginState struct {
	FlowID, Site, Mode string
	Expires            int64
	Interval           uint32
	Nonce              []byte
	Bearer             []byte
}

var stateKeyMu sync.Mutex

func loadStateKey(authDir string) ([]byte, error) {
	if authDir == "" {
		return nil, errors.New("CPA auth directory unavailable")
	}
	stateKeyMu.Lock()
	defer stateKeyMu.Unlock()
	if err := os.MkdirAll(authDir, 0700); err != nil {
		return nil, errors.New("cannot create auth directory")
	}
	path := filepath.Join(authDir, ".zcode-oauth-state.key")
	key, err := os.ReadFile(path)
	if err == nil {
		if len(key) != 32 {
			return nil, errors.New("invalid state encryption key")
		}
		return key, nil
	}
	if !os.IsNotExist(err) {
		return nil, errors.New("cannot read state encryption key")
	}
	key = make([]byte, 32)
	if _, err = io.ReadFull(rand.Reader, key); err != nil {
		return nil, errors.New("cannot create state encryption key")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		key, err = os.ReadFile(path)
		if err != nil || len(key) != 32 {
			return nil, errors.New("cannot read state encryption key")
		}
		return key, nil
	}
	if err != nil {
		return nil, errors.New("cannot persist state encryption key")
	}
	_, err = f.Write(key)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		_ = os.Remove(path)
		return nil, errors.New("cannot persist state encryption key")
	}
	return key, nil
}
func deriveStateValues(bearer []byte) (string, string) {
	hexBearer := hex.EncodeToString(bearer)
	sum := sha256.Sum256(append([]byte("zcode-device-v1\x00"), bearer...))
	d := sum[:16]
	d[6] = (d[6] & 0x0f) | 0x40
	d[8] = (d[8] & 0x3f) | 0x80
	device := fmt.Sprintf("%x-%x-%x-%x-%x", d[:4], d[4:6], d[6:8], d[8:10], d[10:16])
	return hexBearer, device
}
func newNonce() ([]byte, error) {
	b := make([]byte, 12)
	_, err := io.ReadFull(rand.Reader, b)
	return b, err
}
func sealState(key []byte, s loginState) (string, error) {
	if len(s.Nonce) != 12 || len(s.Bearer) != 32 || len(s.FlowID) == 0 || s.Expires <= 0 {
		return "", errors.New("unsupported login flow state")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	flags := byte(0)
	if s.Site == "bigmodel" {
		flags |= 1
	}
	if s.Mode == "team" {
		flags |= 2
	}
	flow := []byte(s.FlowID)
	if len(s.FlowID) == 32 && strings.ToLower(s.FlowID) == s.FlowID {
		if decoded, e := hex.DecodeString(s.FlowID); e == nil {
			flags |= 4
			flow = decoded
		}
	}
	plain := make([]byte, 46+len(flow))
	plain[0] = 1
	plain[1] = flags
	binary.BigEndian.PutUint64(plain[2:10], uint64(s.Expires))
	binary.BigEndian.PutUint32(plain[10:14], s.Interval)
	copy(plain[14:46], s.Bearer)
	copy(plain[46:], flow)
	encoded := base64.RawURLEncoding.EncodeToString(append(append([]byte{}, s.Nonce...), gcm.Seal(nil, s.Nonce, plain, []byte("zcode-login-v1"))...))
	if len(encoded) > 128 {
		return "", errors.New("upstream flow ID exceeds CPA state length limit")
	}
	return encoded, nil
}
func openState(key []byte, encoded string) (loginState, error) {
	var s loginState
	if len(encoded) > 128 {
		return s, errors.New("invalid login state")
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) < 75 {
		return s, errors.New("invalid login state")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return s, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return s, err
	}
	nonce := raw[:12]
	p, err := gcm.Open(nil, nonce, raw[12:], []byte("zcode-login-v1"))
	if err != nil || len(p) < 47 || p[0] != 1 || p[1]&^byte(7) != 0 {
		return s, errors.New("invalid login state")
	}
	flow := string(p[46:])
	if p[1]&4 != 0 {
		if len(p[46:]) != 16 {
			return s, errors.New("invalid login state")
		}
		flow = hex.EncodeToString(p[46:])
	}
	s = loginState{Site: "zai", Mode: "own", Expires: int64(binary.BigEndian.Uint64(p[2:10])), Interval: binary.BigEndian.Uint32(p[10:14]), FlowID: flow, Nonce: append([]byte(nil), nonce...), Bearer: append([]byte(nil), p[14:46]...)}
	if p[1]&1 != 0 {
		s.Site = "bigmodel"
	}
	if p[1]&2 != 0 {
		s.Mode = "team"
	}
	return s, nil
}
