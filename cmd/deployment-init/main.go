// deployment-init creates private local-development secrets and certificates; it never prints their values.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	dir := flag.String("dir", ".cache/deploy", "private output directory")
	flag.Parse()
	if err := initialize(*dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("Local certificates and scoped credentials created. Supply openai_api_key separately; no provider calls made.")
}
func initialize(dir string) error {
	dir = filepath.Clean(dir)
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		return fmt.Errorf("reserve deployment directory without overwriting: %w", err)
	}
	write := func(name string, data []byte) error { return os.WriteFile(filepath.Join(dir, name), data, 0444) }
	serial := func() *big.Int { n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120)); return n }
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "LLM Gateway local CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.AddDate(1, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	if err := write("ca.crt", caPEM); err != nil {
		return err
	}
	caPrivate, err := x509.MarshalECPrivateKey(caKey)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "ca.key"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: caPrivate}), 0600); err != nil {
		return err
	}
	for _, service := range []string{"cache", "ledger"} {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return err
		}
		cert := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: service}, DNSNames: []string{service, "redis", "localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: now.Add(-time.Minute), NotAfter: now.AddDate(0, 3, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		der, err := x509.CreateCertificate(rand.Reader, cert, ca, &key.PublicKey, caKey)
		if err != nil {
			return err
		}
		private, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			return err
		}
		if err := write(service+".crt", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); err != nil {
			return err
		}
		if err := write(service+".key", pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private})); err != nil {
			return err
		}
	}
	secrets := map[string]string{}
	for _, name := range []string{"cache_gateway", "cache_operator", "ledger_gateway", "ledger_operator", "ledger_backup", "pepper"} {
		value := rand.Text()
		secrets[name] = value
		if err := write(name, []byte(value)); err != nil {
			return err
		}
	}
	if err := write("openai_api_key", nil); err != nil {
		return err
	}
	digest := func(name string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(secrets[name]))) }
	commands := "+@read +@write +@connection +@scripting -@dangerous +time"
	cacheACL := fmt.Sprintf("user default off\nuser gateway on #%s %s %%RW~cache:* %%R~rag:* %%RW~quota:* %%R~usage:* %%R~project_key:* ~prompt_cache_v2 ~project_rag_v1 +ft.info +ft.create +ft.search +info\nuser operator on #%s %s ~cache:* ~rag:* ~quota:* %%R~usage:* %%R~project_key:* ~prompt_cache_v2 ~project_rag_v1 +ft.info +ft.create +ft.search +info\n", digest("cache_gateway"), commands, digest("cache_operator"), commands)
	runtimeRules := "+@read +@connection -@dangerous +time +info %R~project_key:* %R~ledger:* (+@write -@dangerous %RW~ledger:*:events %RW~ledger:*:totals %RW~ledger:*:pending %RW~ledger:*:day:*) (+@scripting -@dangerous ~ledger:*)"
	ledgerACL := fmt.Sprintf("user default off\nuser gateway on #%s %s\nuser operator on #%s %s %%RW~ledger:* %%RW~project_key:* +info\nuser backup on #%s -@all +ping +info +bgsave +lastsave +sync +replconf ~*\n", digest("ledger_gateway"), runtimeRules, digest("ledger_operator"), commands, digest("ledger_backup"))
	if err := write("cache.acl", []byte(cacheACL)); err != nil {
		return err
	}
	if err := write("ledger.acl", []byte(ledgerACL)); err != nil {
		return err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	abs = strings.ReplaceAll(abs, "'", "'\"'\"'")
	for _, role := range []string{"gateway", "operator"} {
		data := fmt.Sprintf("export REDIS_ADDR=127.0.0.1:16380\nexport REDIS_USERNAME=%s\nexport REDIS_PASSWORD_FILE='%s/cache_%s'\nexport REDIS_TLS=true\nexport REDIS_TLS_SERVER_NAME=redis\nexport REDIS_TLS_CA='%s/ca.crt'\nexport LEDGER_REDIS_ADDR=127.0.0.1:16381\nexport LEDGER_REDIS_USERNAME=%s\nexport LEDGER_REDIS_PASSWORD_FILE='%s/ledger_%s'\nexport LEDGER_REDIS_TLS=true\nexport LEDGER_REDIS_TLS_SERVER_NAME=ledger\nexport LEDGER_REDIS_TLS_CA='%s/ca.crt'\nexport API_KEY_PEPPER_FILE='%s/pepper'\nexport OPENAI_API_KEY_FILE='%s/openai_api_key'\n", role, abs, role, abs, role, abs, role, abs, abs, abs)
		if err := os.WriteFile(filepath.Join(dir, role+".env"), []byte(data), 0600); err != nil {
			return err
		}
	}
	return nil
}
