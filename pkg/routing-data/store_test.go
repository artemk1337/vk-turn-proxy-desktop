package routingdata

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func bytesField(number uint64, value []byte) []byte {
	result := binary.AppendUvarint(nil, number<<3|2)
	result = binary.AppendUvarint(result, uint64(len(value)))
	return append(result, value...)
}

func intField(number, value uint64) []byte {
	return binary.AppendUvarint(binary.AppendUvarint(nil, number<<3), value)
}

func fixtures() ([]byte, []byte) {
	cidr := append(bytesField(1, []byte{192, 0, 2, 123}), intField(2, 24)...)
	ip := append(bytesField(1, []byte("RU")), bytesField(2, cidr)...)
	domain := append(intField(1, 2), bytesField(2, []byte("example.org"))...)
	site := append(bytesField(1, []byte("example")), bytesField(2, domain)...)
	return bytesField(1, ip), bytesField(1, site)
}

func TestDecodeValidation(t *testing.T) {
	ip, site := fixtures()
	parsed, err := decode(ip, site)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parsed.ip["ru"], []string{"192.0.2.0/24"}) {
		t.Fatal(parsed.ip)
	}
	if !reflect.DeepEqual(parsed.sites["example"], []Domain{{Type: "domain", Value: "example.org"}}) {
		t.Fatal(parsed.sites)
	}
	for _, bad := range [][]byte{nil, {0}, {10, 255}, {8, 1}, append(ip, 0)} {
		if _, err := decode(bad, site); err == nil {
			t.Errorf("accepted malformed input %v", bad)
		}
	}
	invalidCIDR := append(bytesField(1, []byte{192, 0, 2, 1}), intField(2, 33)...)
	if _, err := decode(bytesField(1, append(bytesField(1, []byte("RU")), bytesField(2, invalidCIDR)...)), site); err == nil {
		t.Fatal("accepted invalid prefix")
	}
}

func TestUpdateResolveAndReload(t *testing.T) {
	ip, site := fixtures()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ip" {
			w.Write(ip)
		} else {
			w.Write(site)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	store := NewStore(dir)
	store.urls = [2]string{server.URL + "/ip", server.URL + "/site"}
	catalog, err := store.Update(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.GeoIP) != 1 || catalog.GeoIP[0].ID != "ru" || catalog.GeoIP[0].Group != "Europe" || catalog.UpdatedAt.IsZero() {
		t.Fatal(catalog)
	}
	got, err := NewStore(dir).Resolve([]string{"RU", "ru"}, []string{"example", "example"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Prefixes) != 1 || len(got.Domains) != 1 {
		t.Fatal(got)
	}
	if _, err := store.Resolve([]string{"unknown"}, nil); err == nil {
		t.Fatal("unknown GeoIP accepted")
	}
	if _, err := store.Resolve(nil, []string{"unknown"}); err == nil {
		t.Fatal("unknown GeoSite accepted")
	}
	if _, err := store.Update(context.Background()); err != nil {
		t.Fatal("second update:", err)
	}
}

func TestFailedUpdateKeepsPreviousData(t *testing.T) {
	ip, site := fixtures()
	broken := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if broken {
			w.Write([]byte("invalid"))
			return
		}
		if r.URL.Path == "/ip" {
			w.Write(ip)
		} else {
			w.Write(site)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	store := NewStore(dir)
	store.urls = [2]string{server.URL + "/ip", server.URL + "/site"}
	if _, err := store.Update(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "current.json"))
	if err != nil {
		t.Fatal(err)
	}
	broken = true
	if _, err := store.Update(context.Background()); err == nil {
		t.Fatal("bad update accepted")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "current.json"))
	if string(before) != string(after) {
		t.Fatal("failed update changed manifest")
	}
	if _, err := NewStore(dir).Resolve([]string{"ru"}, []string{"example"}); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "generation-*"))
	if len(files) != 1 {
		t.Fatal("failed generation was retained", files)
	}
}

func TestInverseAndMalformedCache(t *testing.T) {
	ip, site := fixtures()
	data, err := decode(ip, site)
	if err != nil {
		t.Fatal(err)
	}
	data.inverse["ru"] = true
	store := NewStore(t.TempDir())
	store.data = data
	if _, err := store.Resolve([]string{"ru"}, nil); err == nil {
		t.Fatal("inverse GeoIP accepted")
	}
	if len(catalog(store.data, store.updatedAt).GeoIP) != 0 {
		t.Fatal("inverse GeoIP advertised")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "current.json"), []byte(`{"generation":"../outside"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(dir).Catalog(); err == nil {
		t.Fatal("invalid manifest accepted")
	}
}

func TestDownloadFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/large" {
			w.Header().Set("Content-Length", "134217729")
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	store := NewStore(t.TempDir())
	for _, path := range []string{"/large", "/error"} {
		if _, err := store.download(context.Background(), server.URL+path); err == nil {
			t.Fatalf("accepted %s", path)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.download(ctx, server.URL); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestDomainKindsAndWireTypes(t *testing.T) {
	ip, _ := fixtures()
	for kind, name := range []string{"plain", "regex", "domain", "full"} {
		domain := append(intField(1, uint64(kind)), bytesField(2, []byte("example.org"))...)
		site := bytesField(1, append(bytesField(1, []byte("example")), bytesField(2, domain)...))
		data, err := decode(ip, site)
		if err != nil {
			t.Fatal(err)
		}
		if data.sites["example"][0].Type != name {
			t.Fatal(data.sites)
		}
	}
	for _, domain := range [][]byte{
		append(intField(1, 4), bytesField(2, []byte("example.org"))...),
		append(intField(1, 1), bytesField(2, []byte("["))...),
		append(bytesField(1, []byte{2}), bytesField(2, []byte("example.org"))...),
	} {
		site := bytesField(1, append(bytesField(1, []byte("example")), bytesField(2, domain)...))
		if _, err := decode(ip, site); err == nil {
			t.Fatal("invalid domain accepted")
		}
	}
}

func TestReadersUsePreviousPairDuringUpdate(t *testing.T) {
	ip, site := fixtures()
	nextIP := bytes.ReplaceAll(ip, []byte{192, 0, 2, 123}, []byte{198, 51, 100, 123})
	nextSite := bytes.ReplaceAll(site, []byte("example.org"), []byte("example.net"))
	started := make(chan struct{})
	release := make(chan struct{})
	var updating atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if updating.Load() {
			if r.URL.Path == "/ip" {
				close(started)
				<-release
				w.Write(nextIP)
			} else {
				w.Write(nextSite)
			}
			return
		}
		if r.URL.Path == "/ip" {
			w.Write(ip)
		} else {
			w.Write(site)
		}
	}))
	defer server.Close()
	// Release a blocked handler even when a reader times out.
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	dir := t.TempDir()
	store := NewStore(dir)
	store.urls = [2]string{server.URL + "/ip", server.URL + "/site"}
	if _, err := store.Update(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Start with an unloaded Store to exercise reading the previous on-disk pair too.
	store = NewStore(dir)
	store.urls = [2]string{server.URL + "/ip", server.URL + "/site"}
	updating.Store(true)
	done := make(chan error, 1)
	go func() { _, err := store.Update(context.Background()); done <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("update did not reach server")
	}
	read := make(chan error, 1)
	go func() {
		catalog, err := store.Catalog()
		if err != nil {
			read <- err
			return
		}
		if len(catalog.GeoIP) != 1 || len(catalog.GeoSite) != 1 {
			read <- fmt.Errorf("invalid previous catalog: %+v", catalog)
			return
		}
		rules, err := store.Resolve([]string{"ru"}, []string{"example"})
		if err == nil && (!reflect.DeepEqual(rules.Prefixes, []string{"192.0.2.0/24"}) || !reflect.DeepEqual(rules.Domains, []Domain{{Type: "domain", Value: "example.org"}})) {
			err = fmt.Errorf("invalid previous pair: %+v", rules)
		}
		read <- err
	}()
	select {
	case err := <-read:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("readers blocked behind HTTP update")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	rules, err := store.Resolve([]string{"ru"}, []string{"example"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rules.Prefixes, []string{"198.51.100.0/24"}) || !reflect.DeepEqual(rules.Domains, []Domain{{Type: "domain", Value: "example.net"}}) {
		t.Fatalf("invalid updated pair: %+v", rules)
	}
}
