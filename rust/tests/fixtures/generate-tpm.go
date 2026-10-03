//go:build ignore

// Generates signed TPM fixtures and verifies each expected result with the
// application's pinned Go WebAuthn implementation before writing the file.
package main

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/google/go-tpm/tpm2"
)

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

type fixture struct {
	Name      string `json:"name"`
	Key       string `json:"key"`
	Statement string `json:"statement"`
	Message   string `json:"message"`
	AAGUID    string `json:"aaguid"`
	Valid     bool   `json:"valid"`
}

func main() {
	aik := must(rsa.GenerateKey(rand.Reader, 2048))
	credential := must(rsa.GenerateKey(rand.Reader, 2048))
	ec := must(ecdsa.GenerateKey(elliptic.P256(), rand.Reader))
	guid := []byte("0123456789abcdef")
	rawAuth := []byte("synthetic TPM authenticator data")
	clientHash := sha256.Sum256([]byte("synthetic client data"))
	message := append(append([]byte{}, rawAuth...), clientHash[:]...)
	extra := sha256.Sum256(message)
	names := []string{"rsa-default-exponent", "rsa-explicit-exponent", "sha1-name", "sha384-name", "sha512-name", "ec-p256", "noncritical-san-extra-eku", "critical-aaguid", "public-trailing-bytes", "rsa-wrong-exponent", "rsa-wrong-modulus", "ec-wrong-point", "wrong-name", "wrong-extra", "wrong-signature", "wrong-version", "ecdaa", "subject-not-empty", "unknown-manufacturer", "missing-eku", "ca-certificate", "missing-basic-constraints", "expired-certificate", "future-certificate", "wrong-aaguid"}
	var fixtures []fixture
	for index, name := range names {
		valid := index < 9
		manufacturer := "id:49465800"
		if name == "unknown-manufacturer" {
			manufacturer = "id:00000000"
		}
		rdn := pkix.RDNSequence{{{Type: asn1.ObjectIdentifier{2, 23, 133, 2, 1}, Value: manufacturer}, {Type: asn1.ObjectIdentifier{2, 23, 133, 2, 2}, Value: "Fixture TPM"}, {Type: asn1.ObjectIdentifier{2, 23, 133, 2, 3}, Value: "id:00010002"}}}
		san := must(asn1.Marshal([]asn1.RawValue{{Class: 2, Tag: 4, IsCompound: true, Bytes: must(asn1.Marshal(rdn))}}))
		cert := &x509.Certificate{SerialNumber: big.NewInt(int64(index + 1)), NotBefore: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), NotAfter: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC), BasicConstraintsValid: true, UnknownExtKeyUsage: []asn1.ObjectIdentifier{{2, 23, 133, 8, 3}}, ExtraExtensions: []pkix.Extension{{Id: asn1.ObjectIdentifier{2, 5, 29, 17}, Critical: true, Value: san}}}
		switch name {
		case "subject-not-empty":
			cert.Subject.CommonName = "not empty"
		case "noncritical-san-extra-eku":
			cert.ExtraExtensions[0].Critical = false
			cert.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
		case "critical-aaguid", "wrong-aaguid":
			value := append([]byte{}, guid...)
			if name == "wrong-aaguid" {
				value[0] ^= 1
			}
			cert.ExtraExtensions = append(cert.ExtraExtensions, pkix.Extension{Id: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 45724, 1, 1, 4}, Critical: true, Value: must(asn1.Marshal(value))})
		case "missing-eku":
			cert.UnknownExtKeyUsage = nil
		case "ca-certificate":
			cert.IsCA = true
		case "missing-basic-constraints":
			cert.BasicConstraintsValid = false
		case "expired-certificate":
			cert.NotAfter = time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
		case "future-certificate":
			cert.NotBefore = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
		}
		parent := &x509.Certificate{Subject: pkix.Name{CommonName: "Synthetic TPM issuer"}}
		der := must(x509.CreateCertificate(rand.Reader, cert, parent, &aik.PublicKey, aik))
		key := map[int]any{1: 3, 3: -257, -1: credential.N.Bytes(), -2: []byte{1, 0, 1}}
		params := &tpm2.TPMSRSAParms{Symmetric: tpm2.TPMTSymDefObject{Algorithm: tpm2.TPMAlgNull}, Scheme: tpm2.TPMTRSAScheme{Scheme: tpm2.TPMAlgNull}, KeyBits: 2048}
		if name == "rsa-explicit-exponent" {
			params.Exponent = 65537
		}
		if name == "rsa-wrong-exponent" {
			params.Exponent = 3
		}
		modulus := credential.N.Bytes()
		if name == "rsa-wrong-modulus" {
			modulus[0] ^= 1
		}
		public := tpm2.TPMTPublic{Type: tpm2.TPMAlgRSA, NameAlg: tpm2.TPMAlgSHA256, Parameters: tpm2.NewTPMUPublicParms(tpm2.TPMAlgRSA, params), Unique: tpm2.NewTPMUPublicID(tpm2.TPMAlgRSA, &tpm2.TPM2BPublicKeyRSA{Buffer: modulus})}
		switch name {
		case "sha1-name":
			public.NameAlg = tpm2.TPMAlgSHA1
		case "sha384-name":
			public.NameAlg = tpm2.TPMAlgSHA384
		case "sha512-name":
			public.NameAlg = tpm2.TPMAlgSHA512
		}
		if name == "ec-p256" || name == "ec-wrong-point" {
			x, y := ec.X.FillBytes(make([]byte, 32)), ec.Y.FillBytes(make([]byte, 32))
			key = map[int]any{1: 2, 3: -7, -1: 1, -2: append([]byte{}, x...), -3: y}
			if name == "ec-wrong-point" {
				x[0] ^= 1
			}
			public.Type = tpm2.TPMAlgECC
			public.Parameters = tpm2.NewTPMUPublicParms(tpm2.TPMAlgECC, &tpm2.TPMSECCParms{Symmetric: tpm2.TPMTSymDefObject{Algorithm: tpm2.TPMAlgNull}, Scheme: tpm2.TPMTECCScheme{Scheme: tpm2.TPMAlgNull}, CurveID: tpm2.TPMECCNistP256, KDF: tpm2.TPMTKDFScheme{Scheme: tpm2.TPMAlgNull}})
			public.Unique = tpm2.NewTPMUPublicID(tpm2.TPMAlgECC, &tpm2.TPMSECCPoint{X: tpm2.TPM2BECCParameter{Buffer: x}, Y: tpm2.TPM2BECCParameter{Buffer: y}})
		}
		objectName := must(tpm2.ObjectName(&public))
		if name == "wrong-name" {
			objectName.Buffer[3] ^= 1
		}
		boundExtra := append([]byte{}, extra[:]...)
		if name == "wrong-extra" {
			boundExtra[0] ^= 1
		}
		info := tpm2.TPMSAttest{Magic: tpm2.TPMGeneratedValue, Type: tpm2.TPMSTAttestCertify, ExtraData: tpm2.TPM2BData{Buffer: boundExtra}, Attested: tpm2.NewTPMUAttest(tpm2.TPMSTAttestCertify, &tpm2.TPMSCertifyInfo{Name: *objectName})}
		infoBytes := tpm2.Marshal(info)
		digest := sha256.Sum256(infoBytes)
		sig := must(rsa.SignPKCS1v15(rand.Reader, aik, crypto.SHA256, digest[:]))
		if name == "wrong-signature" {
			sig[0] ^= 1
		}
		publicBytes := tpm2.Marshal(public)
		if name == "public-trailing-bytes" {
			publicBytes = append(publicBytes, 1, 2, 3)
		}
		statement := map[string]any{"ver": "2.0", "alg": int64(-257), "x5c": []any{der}, "sig": sig, "certInfo": infoBytes, "pubArea": publicBytes}
		if name == "wrong-version" {
			statement["ver"] = "1.2"
		}
		if name == "ecdaa" {
			statement["ecdaaKeyId"] = []byte{1}
		}
		keyBytes := must(webauthncbor.Marshal(key))
		att := protocol.AttestationObject{Format: "tpm", RawAuthData: rawAuth, AuthData: protocol.AuthenticatorData{AttData: protocol.AttestedCredentialData{AAGUID: guid, CredentialPublicKey: keyBytes}}, AttStatement: statement}
		err := att.VerifyAttestation(clientHash[:], nil, protocol.AttestationPolicy{}, protocol.SignaturePolicy{})
		if (err == nil) != valid {
			panic(fmt.Sprintf("%s: expected valid=%t, got %v", name, valid, err))
		}
		fixtures = append(fixtures, fixture{name, hex.EncodeToString(keyBytes), hex.EncodeToString(must(webauthncbor.Marshal(statement))), hex.EncodeToString(message), hex.EncodeToString(guid), valid})
	}
	data := must(json.MarshalIndent(fixtures, "", "  "))
	data = append(data, '\n')
	if err := os.WriteFile(os.Args[1], data, 0644); err != nil {
		panic(err)
	}
}
