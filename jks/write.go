package jks

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"
)

var (
	// RFC 3279 § 2.3
	oidPublicKeyRSA = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
	asn1NULL        = []byte{0x05, 0x00}
)

func (ks *Keystore) Pack(opts *Options) ([]byte, error) {
	var buf bytes.Buffer
	writeUint32(&buf, MagicNumber)
	writeUint32(&buf, 2) // version
	writeUint32(&buf, uint32(len(ks.Certs)+len(ks.Keypairs)))

	for _, cert := range ks.Certs {
		if err := writeCert(&buf, cert); err != nil {
			return nil, err
		}
	}
	for _, kp := range ks.Keypairs {
		if err := writeKeypair(&buf, kp, opts); err != nil {
			return nil, err
		}
	}

	digest := ComputeDigest(buf.Bytes(), opts.Password)
	buf.Write(digest)
	return buf.Bytes(), nil
}

func writeCert(w io.Writer, cert *Cert) error {
	writeUint32(w, 2) // type = certificate
	if err := writeStr(w, cert.Alias); err != nil {
		return fmt.Errorf("failed to write alias (%v): %q",
			err, cert.Alias)
	}

	ts := cert.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	writeTimestamp(w, ts)

	if err := writeStr(w, CertType); err != nil {
		return fmt.Errorf("failed to write certificate type (%v)", err)
	}

	writeUint32(w, uint32(len(cert.Cert.Raw)))
	w.Write(cert.Cert.Raw)

	return nil
}

func writeKeypair(w io.Writer, kp *Keypair, opts *Options) error {
	writeUint32(w, 1) // type = private key + cert chain
	if err := writeStr(w, kp.Alias); err != nil {
		return fmt.Errorf("failed to write alias (%v): %q",
			err, kp.Alias)
	}

	passwd, ok := opts.KeyPasswords[kp.Alias]
	if !ok {
		passwd = opts.Password
	}

	ts := kp.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	writeTimestamp(w, ts)

	// marshal the key into ‘raw’
	// TODO: need to support non-RSA keys too
	rsaKey := kp.PrivateKey.(*rsa.PrivateKey)
	rawKeyInfo := PrivateKeyInfo{
		Algo: pkix.AlgorithmIdentifier{
			Algorithm: oidPublicKeyRSA,
			Parameters: asn1.RawValue{
				FullBytes: asn1NULL,
			},
		},
		PrivateKey: x509.MarshalPKCS1PrivateKey(rsaKey),
	}
	raw, err := asn1.Marshal(rawKeyInfo)
	if err != nil {
		return fmt.Errorf("failed to marshal private key: %v", err)
	}

	ciphertext, err := EncryptJavaKeyEncryption1(raw, passwd)
	if err != nil {
		return fmt.Errorf("failed to marshal private key: %v", err)
	}
	keyInfo := EncryptedPrivateKeyInfo{
		Algo: pkix.AlgorithmIdentifier{
			Algorithm: JavaKeyEncryptionOID1,
			Parameters: asn1.RawValue{
				FullBytes: asn1NULL,
			},
		},
		EncryptedData: ciphertext,
	}
	raw, err = asn1.Marshal(keyInfo)
	if err != nil {
		return fmt.Errorf("failed to marshal PKCS#8 encrypted "+
			"private key info: %v", err)
	}
	writeUint32(w, uint32(len(raw)))
	w.Write(raw)

	writeUint32(w, uint32(len(kp.CertChain)))
	for _, cert := range kp.CertChain {
		if err := writeStr(w, CertType); err != nil {
			return fmt.Errorf("failed to write certificate "+
				"type (%v)", err)
		}
		writeUint32(w, uint32(len(cert.Cert.Raw)))
		w.Write(cert.Cert.Raw)
	}

	return nil
}

func writeUint32(w io.Writer, u uint32) {
	var raw [4]byte
	binary.BigEndian.PutUint32(raw[:], u)
	w.Write(raw[:])
}

func writeUint64(w io.Writer, u uint64) {
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], u)
	w.Write(raw[:])
}

func writeTimestamp(w io.Writer, ts time.Time) {
	ms := ts.UnixNano() / 1e6
	writeUint64(w, uint64(ms))
}

func writeStr(w io.Writer, s string) error {
	if len(s) > 0xFFFF {
		return errors.New("string too long")
	}

	var raw [2]byte
	binary.BigEndian.PutUint16(raw[:], uint16(len(s)))
	w.Write(raw[:])
	w.Write([]byte(s))
	return nil
}
