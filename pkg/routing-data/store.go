// SPDX-License-Identifier: MIT
package routingdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

const maxDatasetBytes = 128 << 20

type Entry struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Group string `json:"group"`
}

type Catalog struct {
	GeoIP     []Entry   `json:"geoip"`
	GeoSite   []Entry   `json:"geosite"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type manifest struct {
	Generation string    `json:"generation"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type Store struct {
	mu        sync.Mutex
	updateMu  sync.Mutex
	directory string
	client    *http.Client
	urls      [2]string
	data      dataset
	updatedAt time.Time
}

func NewStore(directory string) *Store {
	return &Store{directory: directory, client: &http.Client{Timeout: 120 * time.Second}, urls: [2]string{
		"https://github.com/Loyalsoldier/geoip/releases/latest/download/geoip.dat",
		"https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geosite.dat",
	}}
}

func (s *Store) load() error {
	if s.data.ip != nil {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(s.directory, "current.json"))
	if err != nil {
		return err
	}
	var current manifest
	if err := json.Unmarshal(raw, &current); err != nil {
		return err
	}
	if current.Generation == "" || filepath.Base(current.Generation) != current.Generation || !strings.HasPrefix(current.Generation, "generation-") {
		return errors.New("invalid routing cache generation")
	}
	var contents [2][]byte
	for i, name := range []string{"geoip.dat", "geosite.dat"} {
		file, err := os.Open(filepath.Join(s.directory, current.Generation, name))
		if err != nil {
			return err
		}
		contents[i], err = io.ReadAll(io.LimitReader(file, maxDatasetBytes+1))
		file.Close()
		if err != nil {
			return err
		}
		if len(contents[i]) > maxDatasetBytes {
			return errors.New("routing dataset exceeds size limit")
		}
	}
	s.data, err = decode(contents[0], contents[1])
	if err == nil {
		s.updatedAt = current.UpdatedAt
	}
	return err
}

func (s *Store) Catalog() (Catalog, error) {
	s.mu.Lock()
	if err := s.load(); err != nil {
		s.mu.Unlock()
		return Catalog{}, err
	}
	data, updatedAt := s.data, s.updatedAt
	s.mu.Unlock()
	return catalog(data, updatedAt), nil
}

func catalog(data dataset, updatedAt time.Time) Catalog {
	c := Catalog{GeoIP: []Entry{}, GeoSite: []Entry{}, UpdatedAt: updatedAt}
	for id := range data.ip {
		// Inverse datasets require complement semantics and are intentionally unavailable.
		if data.inverse[id] {
			continue
		}
		name := id
		if region, err := language.ParseRegion(strings.ToUpper(id)); err == nil && len(id) == 2 {
			name = display.English.Regions().Name(region)
		}
		c.GeoIP = append(c.GeoIP, Entry{ID: id, Name: name, Group: ipGroup(id)})
	}
	for id := range data.sites {
		c.GeoSite = append(c.GeoSite, Entry{ID: id, Name: id, Group: siteGroup(id)})
	}
	less := func(a, b Entry) int {
		if n := strings.Compare(a.Group, b.Group); n != 0 {
			return n
		}
		return strings.Compare(a.Name, b.Name)
	}
	slices.SortFunc(c.GeoIP, less)
	slices.SortFunc(c.GeoSite, less)
	return c
}

func (s *Store) Resolve(geoipTags, geositeTags []string) (Rules, error) {
	s.mu.Lock()
	if err := s.load(); err != nil {
		s.mu.Unlock()
		return Rules{}, err
	}
	data := s.data
	s.mu.Unlock()
	result := Rules{Prefixes: []string{}, Domains: []Domain{}}
	seenIPs := make(map[string]bool)
	seenDomains := make(map[Domain]bool)
	for _, tag := range geoipTags {
		id := strings.ToLower(tag)
		prefixes, ok := data.ip[id]
		if !ok {
			return Rules{}, fmt.Errorf("unknown GeoIP tag %q", tag)
		}
		if data.inverse[id] {
			return Rules{}, fmt.Errorf("inverse GeoIP tag %q is unsupported", tag)
		}
		for _, prefix := range prefixes {
			if !seenIPs[prefix] {
				result.Prefixes = append(result.Prefixes, prefix)
				seenIPs[prefix] = true
			}
		}
	}
	for _, tag := range geositeTags {
		domains, ok := data.sites[strings.ToLower(tag)]
		if !ok {
			return Rules{}, fmt.Errorf("unknown GeoSite tag %q", tag)
		}
		for _, domain := range domains {
			if !seenDomains[domain] {
				result.Domains = append(result.Domains, domain)
				seenDomains[domain] = true
			}
		}
	}
	return result, nil
}

func (s *Store) Update(ctx context.Context) (Catalog, error) {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	if err := os.MkdirAll(s.directory, 0700); err != nil {
		return Catalog{}, err
	}
	previous := manifest{}
	if raw, err := os.ReadFile(filepath.Join(s.directory, "current.json")); err == nil {
		_ = json.Unmarshal(raw, &previous)
	}
	dir, err := os.MkdirTemp(s.directory, "generation-")
	if err != nil {
		return Catalog{}, err
	}
	committed := false
	defer func() {
		if !committed {
			os.RemoveAll(dir)
		}
	}()
	var contents [2][]byte
	for i, name := range []string{"geoip.dat", "geosite.dat"} {
		contents[i], err = s.download(ctx, s.urls[i])
		if err != nil {
			return Catalog{}, fmt.Errorf("download %s: %w", name, err)
		}
		file, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return Catalog{}, err
		}
		if _, err = file.Write(contents[i]); err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err != nil {
			return Catalog{}, err
		}
		if closeErr != nil {
			return Catalog{}, closeErr
		}
	}
	data, err := decode(contents[0], contents[1])
	if err != nil {
		return Catalog{}, fmt.Errorf("decode routing data: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Catalog{}, err
	}
	current := manifest{Generation: filepath.Base(dir), UpdatedAt: time.Now().UTC()}
	raw, err := json.Marshal(current)
	if err != nil {
		return Catalog{}, err
	}
	file, err := os.CreateTemp(s.directory, ".current-*")
	if err != nil {
		return Catalog{}, err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return Catalog{}, err
	}
	if closeErr != nil {
		return Catalog{}, closeErr
	}
	s.mu.Lock()
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		return Catalog{}, err
	}
	if err := os.Rename(file.Name(), filepath.Join(s.directory, "current.json")); err != nil {
		s.mu.Unlock()
		return Catalog{}, err
	}
	committed = true
	s.data = data
	s.updatedAt = current.UpdatedAt
	s.mu.Unlock()
	// Retain the previous generation and recent files for concurrent readers/updaters.
	entries, _ := os.ReadDir(s.directory)
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "generation-") || entry.Name() == current.Generation || entry.Name() == previous.Generation {
			continue
		}
		if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) > 48*time.Hour {
			_ = os.RemoveAll(filepath.Join(s.directory, entry.Name()))
		}
	}
	return catalog(data, current.UpdatedAt), nil
}

func (s *Store) download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maxDatasetBytes {
		return nil, errors.New("routing dataset exceeds size limit")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxDatasetBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxDatasetBytes {
		return nil, errors.New("routing dataset exceeds size limit")
	}
	return data, nil
}

func ipGroup(id string) string {
	groups := []struct{ name, codes string }{
		{"Europe", "ad al at ax ba be bg by ch cz de dk ee es fi fo fr gb gg gi gr hr hu ie im is it je li lt lu lv mc md me mk mt nl no pl pt ro rs ru se si sj sk sm ua va"},
		{"Asia", "ae af am az bd bh bn bt cn cy ge hk id il in iq ir jo jp kg kh kp kr kw kz la lb lk mm mn mo mv my np om ph pk ps qa sa sg sy th tj tl tm tr tw uz vn ye"},
		{"Americas", "ag ai ar aw bb bl bm bo bq br bs bz ca cl co cr cu cw dm do ec fk gd gf gl gp gt gy hn ht jm kn ky lc mf mq ms mx ni pa pe pm pr py sr sv sx tc tt us uy vc ve vg vi"},
		{"Africa", "ao bf bi bj bw cd cf cg ci cm cv dj dz eg eh er et ga gh gm gn gq gw ke km lr ls ly ma mg ml mr mu mw mz na ne ng re rw sc sd sh sl sn so ss st sz td tg tn tz ug yt za zm zw"},
		{"Oceania", "as au cc ck cx fj fm gu ki mh mp nc nf nr nu nz pf pg pn pw sb tk to tv um vu wf ws"},
	}
	for _, group := range groups {
		if slices.Contains(strings.Fields(group.codes), id) {
			return group.name
		}
	}
	return "Other networks"
}

func siteGroup(id string) string {
	switch {
	case strings.HasPrefix(id, "category-"):
		return "Categories"
	case strings.HasPrefix(id, "geolocation-") || strings.HasPrefix(id, "tld-"):
		return "Regional"
	case strings.Contains(id, "ads") || id == "tracking":
		return "Advertising and tracking"
	case slices.Contains([]string{"youtube", "netflix", "twitch", "spotify", "bilibili", "hulu", "disney"}, id):
		return "Media"
	case slices.Contains([]string{"telegram", "discord", "whatsapp", "facebook", "instagram", "twitter", "vk", "tiktok"}, id):
		return "Communication"
	case slices.Contains([]string{"github", "gitlab", "stackoverflow", "docker", "jetbrains"}, id):
		return "Development"
	case slices.Contains([]string{"steam", "epicgames", "ea", "riot", "blizzard", "playstation", "xbox", "nintendo"}, id):
		return "Games"
	default:
		return "Services"
	}
}
