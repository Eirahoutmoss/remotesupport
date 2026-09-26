// Package e2e provides the end-to-end encrypted channel between agent and
// operator. The relay forwards only ciphertext.
//
// Handshake: each side sends an ephemeral X25519 public key. Keys are derived
// with HKDF-SHA256 over the shared secret, salted with the connection code
// and bound to both public keys. A short authentication string (SAS) is
// derived as well; both sides display it so a man-in-the-middle (including a
// malicious relay) can be detected by comparing it out of band.
//
// Transport: AES-256-GCM, one key per direction, 96-bit nonces built from a
// strictly increasing counter. The underlying websocket is ordered, so any
// replay, drop or reorder fails authentication.
package e2e

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
)

// Roles.
const (
	RoleAgent    byte = 'A'
	RoleOperator byte = 'O'
)

var magic = []byte("RS1")

// Transport carries whole binary messages in order.
type Transport interface {
	ReadMsg(ctx context.Context) ([]byte, error)
	WriteMsg(ctx context.Context, b []byte) error
}

// Channel is an established encrypted channel.
type Channel struct {
	t    Transport
	send cipher.AEAD
	recv cipher.AEAD

	wmu   sync.Mutex
	sendN uint64
	recvN uint64 // only touched by the single reader

	// SAS is a 6-digit verification code, identical on both ends only if no
	// one intercepted the key exchange.
	SAS string
}

// Handshake performs the key exchange over t.
func Handshake(ctx context.Context, t Transport, role byte, code string) (*Channel, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	myPub := priv.PublicKey().Bytes()
	hello := append(append(append([]byte{}, magic...), role), myPub...)
	if err := t.WriteMsg(ctx, hello); err != nil {
		return nil, err
	}
	peer, err := t.ReadMsg(ctx)
	if err != nil {
		return nil, err
	}
	if len(peer) != len(hello) || !bytes.Equal(peer[:3], magic) || peer[3] == role ||
		(peer[3] != RoleAgent && peer[3] != RoleOperator) {
		return nil, errors.New("e2e: invalid handshake")
	}
	peerPub, err := ecdh.X25519().NewPublicKey(peer[4:])
	if err != nil {
		return nil, err
	}
	shared, err := priv.ECDH(peerPub)
	if err != nil {
		return nil, err
	}

	agentPub, opPub := myPub, peer[4:]
	if role == RoleOperator {
		agentPub, opPub = opPub, agentPub
	}
	info := "remotesupport/v1|" + string(agentPub) + string(opPub)
	km, err := hkdf.Key(sha256.New, shared, []byte(code), info, 68)
	if err != nil {
		return nil, err
	}
	a2o, err := newAEAD(km[0:32])
	if err != nil {
		return nil, err
	}
	o2a, err := newAEAD(km[32:64])
	if err != nil {
		return nil, err
	}
	sas := binary.BigEndian.Uint32(km[64:68]) % 1_000_000

	c := &Channel{t: t, SAS: fmt.Sprintf("%03d %03d", sas/1000, sas%1000)}
	if role == RoleAgent {
		c.send, c.recv = a2o, o2a
	} else {
		c.send, c.recv = o2a, a2o
	}
	return c, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

func nonce(n uint64) []byte {
	var b [12]byte
	binary.BigEndian.PutUint64(b[4:], n)
	return b[:]
}

// Send encrypts and writes one message. Safe for concurrent use.
func (c *Channel) Send(ctx context.Context, p []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	ct := c.send.Seal(nil, nonce(c.sendN), p, nil)
	c.sendN++
	return c.t.WriteMsg(ctx, ct)
}

// Recv reads and decrypts one message. Must be called from one goroutine.
func (c *Channel) Recv(ctx context.Context) ([]byte, error) {
	ct, err := c.t.ReadMsg(ctx)
	if err != nil {
		return nil, err
	}
	p, err := c.recv.Open(nil, nonce(c.recvN), ct, nil)
	if err != nil {
		return nil, errors.New("e2e: authentication failed")
	}
	c.recvN++
	return p, nil
}
