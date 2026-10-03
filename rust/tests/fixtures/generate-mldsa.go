//go:build ignore

// Run from the repository root with Go 1.27+: go run rust/tests/fixtures/generate-mldsa.go
// Creates public certificate/signature fixtures; private key material is discarded.
package main

import (
	"crypto/mldsa"
	"crypto/rand"
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
func main() {
	rootKey := must(mldsa.GenerateKey(mldsa.MLDSA65()))
	leafKey := must(mldsa.GenerateKey(mldsa.MLDSA44()))
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2125, 1, 1, 0, 0, 0, 0, time.UTC)
	rootTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ML-DSA Test Root"}, NotBefore: start, NotAfter: end, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	rootDER := must(x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, rootKey.PublicKey(), rootKey))
	root := must(x509.ParseCertificate(rootDER))
	aaguid := make([]byte, 16)
	aaguidDER := must(asn1.Marshal(aaguid))
	profile := func() *x509.Certificate {
		return &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{Country: []string{"US"}, Organization: []string{"Fixture Vendor"}, OrganizationalUnit: []string{"Authenticator Attestation"}, CommonName: "ML-DSA Test Leaf"}, NotBefore: start, NotAfter: end, BasicConstraintsValid: true, ExtraExtensions: []pkix.Extension{{Id: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 45724, 1, 1, 4}, Value: aaguidDER}}}
	}
	create := func(c *x509.Certificate) []byte {
		return must(x509.CreateCertificate(rand.Reader, c, root, leafKey.PublicKey(), rootKey))
	}
	leafDER := create(profile())
	message := []byte("authenticator data followed by SHA-256 client data hash")
	signature := must(leafKey.Sign(nil, message, &mldsa.Options{}))
	fixture := map[string][]byte{"root": rootDER, "leaf": leafDER, "message": message, "signature": signature, "aaguid": aaguid}
	variants := map[string]func(*x509.Certificate){
		"bad_country":         func(c *x509.Certificate) { c.Subject.Country = []string{"zz"} },
		"bad_organization":    func(c *x509.Certificate) { c.Subject.Organization = nil },
		"bad_unit":            func(c *x509.Certificate) { c.Subject.OrganizationalUnit = []string{"Untrusted"} },
		"bad_common_name":     func(c *x509.Certificate) { c.Subject.CommonName = "" },
		"bad_ca":              func(c *x509.Certificate) { c.IsCA = true },
		"bad_critical_aaguid": func(c *x509.Certificate) { c.ExtraExtensions[0].Critical = true },
		"expired":             func(c *x509.Certificate) { c.NotAfter = time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC) },
	}
	for name, change := range variants {
		c := profile()
		change(c)
		fixture[name] = create(c)
	}
	encoded := must(json.MarshalIndent(fixture, "", "  "))
	if err := os.WriteFile("rust/tests/fixtures/mldsa-packed.json", append(encoded, '\n'), 0644); err != nil {
		panic(err)
	}
}
