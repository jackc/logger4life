//go:build ignore

// Run from repository root with Go 1.27+: go run rust/tests/fixtures/generate-platform.go
// Public attestation fixtures only; all private keys are discarded.
package main

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/mldsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/json"
	"math/big"
	"os"
	"time"
)

func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}
func der(value any) []byte { return must(asn1.Marshal(value)) }
func tagged(tag int, value any) asn1.RawValue {
	return asn1.RawValue{Class: 2, Tag: tag, IsCompound: true, Bytes: der(value)}
}

type authorization struct {
	Purpose asn1.RawValue
	Origin  asn1.RawValue
}
type description struct {
	Version     int
	Security    asn1.Enumerated
	KeyVersion  int
	KeySecurity asn1.Enumerated
	Challenge   []byte
	Unique      []byte
	Software    asn1.RawValue
	Tee         asn1.RawValue
}

func sequence(fields ...asn1.RawValue) asn1.RawValue {
	var data []byte
	for _, v := range fields {
		data = append(data, der(v)...)
	}
	return asn1.RawValue{Tag: 16, IsCompound: true, Bytes: data}
}
func main() {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2125, 1, 1, 0, 0, 0, 0, time.UTC)
	ecRoot := must(ecdsa.GenerateKey(elliptic.P256(), rand.Reader))
	pqRoot := must(mldsa.GenerateKey(mldsa.MLDSA65()))
	pqLeaf := must(mldsa.GenerateKey(mldsa.MLDSA44()))
	ecLeaf := must(ecdsa.GenerateKey(elliptic.P256(), rand.Reader))
	rootTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Platform Fixture Root"}, NotBefore: start, NotAfter: end, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	createRoot := func(key crypto.Signer) []byte {
		return must(x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, key.Public(), key))
	}
	ecRootDER, pqRootDER := createRoot(ecRoot), createRoot(pqRoot)
	ecCert, pqCert := must(x509.ParseCertificate(ecRootDER)), must(x509.ParseCertificate(pqRootDER))
	hash := sha256.Sum256([]byte("webauthn.create client data"))
	message := append([]byte("authenticator data"), hash[:]...)
	nonce := sha256.Sum256(message)
	apple := der(struct {
		Nonce []byte `asn1:"tag:1,explicit"`
	}{nonce[:]})
	purpose := asn1.RawValue{Class: 2, Tag: 1, IsCompound: true, Bytes: der(asn1.RawValue{Tag: 17, IsCompound: true, Bytes: der(2)})}
	origin := tagged(702, 0)
	desc := func() description {
		return description{1, 1, 1, 1, hash[:], []byte{}, sequence(), sequence(purpose, origin)}
	}
	android := der(desc())
	leafTemplate := func(extension []byte, oid asn1.ObjectIdentifier) *x509.Certificate {
		return &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "Platform Fixture Credential"}, NotBefore: start, NotAfter: end, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtraExtensions: []pkix.Extension{{Id: oid, Value: extension}}}
	}
	appleOID := asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 8, 2}
	androidOID := asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 1, 17}
	sign := func(c *x509.Certificate, public any, root *x509.Certificate, key crypto.Signer) []byte {
		return must(x509.CreateCertificate(rand.Reader, c, root, public, key))
	}
	fixture := map[string][]byte{"ec_root": ecRootDER, "pq_root": pqRootDER, "public": pqLeaf.PublicKey().Bytes(), "message": message, "hash": hash[:], "signature": must(pqLeaf.Sign(nil, message, &mldsa.Options{})), "ec_x": ecLeaf.X.FillBytes(make([]byte, 32)), "ec_y": ecLeaf.Y.FillBytes(make([]byte, 32))}
	expiredRootTemplate := *rootTemplate
	expiredRootTemplate.SerialNumber = big.NewInt(3)
	expiredRootTemplate.NotAfter = time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	fixture["expired_root"] = must(x509.CreateCertificate(rand.Reader, &expiredRootTemplate, &expiredRootTemplate, ecRoot.Public(), ecRoot))
	digest := sha256.Sum256(message)
	fixture["ec_signature"] = must(ecdsa.SignASN1(rand.Reader, ecLeaf, digest[:]))
	u2fRP := sha256.Sum256([]byte("u2f.example.com"))
	u2fID := []byte("public credential identifier")
	u2fMessage := append([]byte{0}, u2fRP[:]...)
	u2fMessage = append(u2fMessage, hash[:]...)
	u2fMessage = append(u2fMessage, u2fID...)
	u2fMessage = append(u2fMessage, 4)
	u2fMessage = append(u2fMessage, fixture["ec_x"]...)
	u2fMessage = append(u2fMessage, fixture["ec_y"]...)
	u2fDigest := sha256.Sum256(u2fMessage)
	fixture["u2f_signature"] = must(ecdsa.SignASN1(rand.Reader, ecLeaf, u2fDigest[:]))
	fixture["u2f_rp_hash"] = u2fRP[:]
	fixture["u2f_id"] = u2fID
	for name, extension := range map[string][]byte{"apple": apple, "android": android} {
		oid := appleOID
		if name == "android" {
			oid = androidOID
		}
		fixture[name] = sign(leafTemplate(extension, oid), pqLeaf.PublicKey(), ecCert, ecRoot)
		fixture[name+"_pq_chain"] = sign(leafTemplate(extension, oid), pqLeaf.PublicKey(), pqCert, pqRoot)
		fixture[name+"_ec"] = sign(leafTemplate(extension, oid), &ecLeaf.PublicKey, ecCert, ecRoot)
		invalid := leafTemplate(extension, oid)
		invalid.NotBefore = time.Date(2120, 1, 1, 0, 0, 0, 0, time.UTC)
		fixture[name+"_future"] = sign(invalid, pqLeaf.PublicKey(), ecCert, ecRoot)
		expired := leafTemplate(extension, oid)
		expired.NotAfter = time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
		fixture[name+"_expired"] = sign(expired, pqLeaf.PublicKey(), ecCert, ecRoot)
		fixture[name+"_missing_extension"] = sign(leafTemplate(nil, asn1.ObjectIdentifier{1, 2, 3, 4}), pqLeaf.PublicKey(), ecCert, ecRoot)
	}
	fixture["apple_bad_nonce"] = sign(leafTemplate(der(struct {
		Nonce []byte `asn1:"tag:1,explicit"`
	}{[]byte("wrong")}), appleOID), pqLeaf.PublicKey(), ecCert, ecRoot)
	changes := map[string]func(*description){
		"bad_challenge":     func(d *description) { d.Challenge = []byte("wrong") },
		"software_all_apps": func(d *description) { d.Software = sequence(tagged(600, asn1.RawValue{Tag: 5})) },
		"tee_all_apps":      func(d *description) { d.Tee = sequence(purpose, tagged(600, asn1.RawValue{Tag: 5}), origin) },
		"no_origin":         func(d *description) { d.Tee = sequence(purpose) },
		"bad_origin":        func(d *description) { d.Tee = sequence(purpose, tagged(702, 2)) },
		"malformed_origin":  func(d *description) { d.Tee = sequence(purpose, tagged(702, []byte{0})) },
		"bad_purpose": func(d *description) {
			d.Tee = sequence(tagged(1, asn1.RawValue{Tag: 17, IsCompound: true, Bytes: der(3)}), origin)
		},
		"no_purpose":     func(d *description) { d.Tee = sequence(origin) },
		"software_only":  func(d *description) { d.Software = d.Tee; d.Tee = sequence() },
		"unknown_before": func(d *description) { d.Tee = sequence(purpose, tagged(650, 0), origin) },
		"unknown_after":  func(d *description) { d.Tee = sequence(purpose, origin, tagged(800, 0)) },
	}
	for name, change := range changes {
		d := desc()
		change(&d)
		fixture["android_"+name] = sign(leafTemplate(der(d), androidOID), pqLeaf.PublicKey(), ecCert, ecRoot)
	}
	encoded := must(json.MarshalIndent(fixture, "", "  "))
	if err := os.WriteFile("rust/tests/fixtures/platform-attestations.json", append(encoded, '\n'), 0644); err != nil {
		panic(err)
	}
}
