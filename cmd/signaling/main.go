package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	server "github.com/eirahoutmoss/remotesupport/server/signaling"
	"github.com/eirahoutmoss/remotesupport/shared/landisc"
)

// Environment:
//
//	REMOTESUPPORT_SIGNALING_ADDR  plain ws listener (default 0.0.0.0:8091, "off" disables)
//	REMOTESUPPORT_TLS_ADDR        wss listener (default 0.0.0.0:8443 when TLS is configured)
//	REMOTESUPPORT_TLS_CERT/_KEY   PEM files (e.g. Let's Encrypt); or
//	REMOTESUPPORT_TLS_DIR         directory for an auto-generated self-signed
//	                              certificate; clients pin it with #pin=… in the URL
//	REMOTESUPPORT_TURN_SECRET     coturn static-auth-secret → per-session TURN credentials
//	REMOTESUPPORT_TURN_URLS       comma-separated turn: URLs handed to clients
//	REMOTESUPPORT_TURN_TTL        credential lifetime (Go duration, default 12h)
//	REMOTESUPPORT_MIN_CLIENT_V    reject clients below this protocol version (default 0)
//	REMOTESUPPORT_DEVICE_STORE    JSON file persisting device-access passwords (technician access)
//	REMOTESUPPORT_DISCOVERY       "off" disables LAN discovery on UDP 8090
func main() {
	s := server.NewServer()
	s.SetLogger(log.Printf)
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("REMOTESUPPORT_MIN_CLIENT_V"))); err == nil && v > 0 {
		s.SetMinClientVersion(v)
		log.Printf("En düşük istemci protokol sürümü: %d", v)
	}
	if minApp, latestApp := strings.TrimSpace(os.Getenv("REMOTESUPPORT_MIN_APP")), strings.TrimSpace(os.Getenv("REMOTESUPPORT_LATEST_APP")); minApp != "" || latestApp != "" {
		s.SetAppVersions(minApp, latestApp)
		log.Printf("Sürüm bildirimi: en düşük=%q en son=%q", minApp, latestApp)
	}
	if secret := strings.TrimSpace(os.Getenv("REMOTESUPPORT_TURN_SECRET")); secret != "" {
		urls := strings.FieldsFunc(os.Getenv("REMOTESUPPORT_TURN_URLS"), func(r rune) bool { return r == ',' || r == ' ' || r == ';' })
		ttl, _ := time.ParseDuration(strings.TrimSpace(os.Getenv("REMOTESUPPORT_TURN_TTL")))
		if len(urls) == 0 {
			log.Printf("UYARI: REMOTESUPPORT_TURN_SECRET var ama REMOTESUPPORT_TURN_URLS boş — TURN kimliği verilmeyecek")
		} else {
			s.SetTURN(urls, secret, ttl)
			log.Printf("Oturum başına TURN kimliği etkin: %v", urls)
		}
	}
	if path := strings.TrimSpace(os.Getenv("REMOTESUPPORT_DEVICE_STORE")); path != "" {
		s.SetDeviceStore(path)
		log.Printf("Cihaz kayıt defteri: %s", path)
	}
	mux := s.Handler()

	tlsCfg, pin := loadTLS()
	if tlsCfg != nil {
		addr := envOr("REMOTESUPPORT_TLS_ADDR", "0.0.0.0:8443")
		if pin != "" {
			log.Printf("Sertifika pini: %s", pin)
			log.Printf("İstemci adresi: wss://SUNUCU_IP:%s/v1/ws#pin=%s", portOf(addr), pin)
		}
		go func() {
			srv := &http.Server{Addr: addr, Handler: mux, TLSConfig: tlsCfg, ReadHeaderTimeout: 10 * time.Second}
			log.Printf("NexDesk signaling (TLS) dinleniyor: wss://%s/v1/ws", addr)
			log.Fatal(srv.ListenAndServeTLS("", ""))
		}()
	}

	if !strings.EqualFold(os.Getenv("REMOTESUPPORT_DISCOVERY"), "off") {
		go landisc.Serve(log.Printf)
	}

	addr := envOr("REMOTESUPPORT_SIGNALING_ADDR", "0.0.0.0:8091")
	if strings.EqualFold(addr, "off") {
		if tlsCfg == nil {
			log.Fatal("Düz ws kapalı ve TLS yapılandırılmamış: dinlenecek port yok")
		}
		select {}
	}
	log.Printf("NexDesk signaling (şifresiz) dinleniyor: ws://%s/v1/ws", addr)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func portOf(addr string) string {
	if _, p, err := net.SplitHostPort(addr); err == nil {
		return p
	}
	return addr
}

// loadTLS returns nil when TLS is not configured. pin is the base64url SHA-256
// of the certificate's public key (SPKI), "" for CA-issued certificates.
func loadTLS() (*tls.Config, string) {
	certFile, keyFile := os.Getenv("REMOTESUPPORT_TLS_CERT"), os.Getenv("REMOTESUPPORT_TLS_KEY")
	if certFile == "" || keyFile == "" {
		dir := strings.TrimSpace(os.Getenv("REMOTESUPPORT_TLS_DIR"))
		if dir == "" {
			return nil, ""
		}
		certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
		if _, err := os.Stat(certFile); err != nil {
			if err := generateSelfSigned(certFile, keyFile); err != nil {
				log.Fatalf("Sertifika üretilemedi: %v", err)
			}
			log.Printf("Yeni öz-imzalı sertifika üretildi: %s", certFile)
		}
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		log.Fatalf("TLS sertifikası okunamadı: %v", err)
	}
	pin := ""
	if leaf, err := x509.ParseCertificate(cert.Certificate[0]); err == nil {
		sum := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
		pin = base64.RawURLEncoding.EncodeToString(sum[:])
		if dir := filepath.Dir(certFile); os.Getenv("REMOTESUPPORT_TLS_DIR") != "" {
			_ = os.WriteFile(filepath.Join(dir, "pin.txt"), []byte(pin+"\n"), 0644)
		}
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, pin
}

func generateSelfSigned(certFile, keyFile string) error {
	if err := os.MkdirAll(filepath.Dir(certFile), 0700); err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "NexDesk signaling"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), 0600); err != nil {
		return err
	}
	return os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644)
}
