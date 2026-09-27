package backup

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

// Encrypted backups: a header, then the archive in authenticated chunks.
//
//	"PURROSENC1\n" | salt (16) | argon2 time (4) | memory KiB (4) | threads (1)
//	chunks: length (4, big endian) | AES-256-GCM(chunk) with nonce = counter,
//	and the additional data marking the last chunk, so a truncated file is
//	detected rather than restored partially.
const (
	encMagic  = "PURROSENC1\n"
	chunkSize = 64 << 10
	saltSize  = 16
)

var (
	// ErrPassphrase is returned when a backup can't be decrypted.
	ErrPassphrase = errors.New("wrong passphrase, or the backup is damaged")
	errTruncated  = errors.New("the backup file is truncated")
)

type kdfParams struct {
	time, memory uint32
	threads      uint8
}

var defaultKDF = kdfParams{time: 3, memory: 64 * 1024, threads: 4}

func deriveKey(pass string, salt []byte, p kdfParams) []byte {
	return argon2.IDKey([]byte(pass), salt, p.time, p.memory, p.threads, 32)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func nonce(counter uint64) []byte {
	n := make([]byte, 12)
	binary.BigEndian.PutUint64(n[4:], counter)
	return n
}

var (
	adMiddle = []byte{0}
	adFinal  = []byte{1}
)

// encWriter encrypts everything written to it. Close writes the final chunk.
type encWriter struct {
	w       io.Writer
	aead    cipher.AEAD
	buf     []byte
	counter uint64
	closed  bool
}

func newEncWriter(w io.Writer, pass string) (*encWriter, error) {
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	aead, err := newGCM(deriveKey(pass, salt, defaultKDF))
	if err != nil {
		return nil, err
	}
	hdr := bytes.NewBufferString(encMagic)
	hdr.Write(salt)
	_ = binary.Write(hdr, binary.BigEndian, defaultKDF.time)
	_ = binary.Write(hdr, binary.BigEndian, defaultKDF.memory)
	hdr.WriteByte(defaultKDF.threads)
	if _, err := w.Write(hdr.Bytes()); err != nil {
		return nil, err
	}
	return &encWriter{w: w, aead: aead}, nil
}

func (e *encWriter) Write(p []byte) (int, error) {
	n := len(p)
	e.buf = append(e.buf, p...)
	// Keep at least one byte buffered so the last chunk is written by Close.
	for len(e.buf) > chunkSize {
		if err := e.flush(e.buf[:chunkSize], adMiddle); err != nil {
			return 0, err
		}
		e.buf = e.buf[chunkSize:]
	}
	return n, nil
}

func (e *encWriter) flush(chunk, ad []byte) error {
	sealed := e.aead.Seal(nil, nonce(e.counter), chunk, ad)
	e.counter++
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(sealed)))
	if _, err := e.w.Write(l[:]); err != nil {
		return err
	}
	_, err := e.w.Write(sealed)
	return err
}

func (e *encWriter) Close() error {
	if e.closed {
		return nil
	}
	e.closed = true
	return e.flush(e.buf, adFinal)
}

// decReader decrypts a stream written by encWriter.
type decReader struct {
	r       *bufio.Reader
	aead    cipher.AEAD
	buf     []byte
	counter uint64
	done    bool
}

// isEncrypted peeks at the header.
func isEncrypted(r *bufio.Reader) bool {
	b, err := r.Peek(len(encMagic))
	return err == nil && string(b) == encMagic
}

func newDecReader(r *bufio.Reader, pass string) (*decReader, error) {
	hdr := make([]byte, len(encMagic)+saltSize+9)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return nil, errTruncated
	}
	salt := hdr[len(encMagic) : len(encMagic)+saltSize]
	rest := hdr[len(encMagic)+saltSize:]
	p := kdfParams{time: binary.BigEndian.Uint32(rest[0:4]), memory: binary.BigEndian.Uint32(rest[4:8]), threads: rest[8]}
	if p.time == 0 || p.time > 20 || p.memory < 8*1024 || p.memory > 4*1024*1024 || p.threads == 0 {
		return nil, fmt.Errorf("unsupported encryption parameters")
	}
	aead, err := newGCM(deriveKey(pass, salt, p))
	if err != nil {
		return nil, err
	}
	return &decReader{r: r, aead: aead}, nil
}

func (d *decReader) Read(p []byte) (int, error) {
	for len(d.buf) == 0 {
		if d.done {
			return 0, io.EOF
		}
		var l [4]byte
		if _, err := io.ReadFull(d.r, l[:]); err != nil {
			return 0, errTruncated
		}
		n := binary.BigEndian.Uint32(l[:])
		if n > chunkSize+uint32(d.aead.Overhead()) {
			return 0, ErrPassphrase
		}
		sealed := make([]byte, n)
		if _, err := io.ReadFull(d.r, sealed); err != nil {
			return 0, errTruncated
		}
		// The last chunk is authenticated as such.
		plain, err := d.aead.Open(nil, nonce(d.counter), sealed, adMiddle)
		if err != nil {
			plain, err = d.aead.Open(nil, nonce(d.counter), sealed, adFinal)
			if err != nil {
				return 0, ErrPassphrase
			}
			d.done = true
			if _, err := d.r.Peek(1); err != io.EOF {
				return 0, errors.New("unexpected data after the end of the backup")
			}
		}
		d.counter++
		d.buf = plain
	}
	n := copy(p, d.buf)
	d.buf = d.buf[n:]
	return n, nil
}
