package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image/png"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const version = "0.1.0"
const userAgent = "Mixtape/" + version + " (OnionOS community port browser)"

type Config struct {
	CatalogURL  string `json:"catalog_url"`
	GitHubToken string `json:"github_token"`
}

type Env struct {
	SDRoot     string
	AppDir     string
	DataDir    string
	Config     Config
	HTTP       *http.Client
	Offline    bool
	LastNetErr error
	APIBase    string // overridable for tests
	dirCache   map[string][]string
}

func newEnv() *Env {
	exe, _ := os.Executable()
	appDir := filepath.Dir(exe)
	sd := os.Getenv("MIXTAPE_SDROOT")
	if sd == "" {
		sd = "/mnt/SDCARD"
	}
	env := &Env{SDRoot: sd, AppDir: appDir, DataDir: filepath.Join(appDir, "data"), dirCache: map[string][]string{}}
	if d := os.Getenv("MIXTAPE_DATA"); d != "" {
		env.DataDir = d
	}
	os.MkdirAll(env.DataDir, 0o755)
	env.Config = Config{CatalogURL: defaultCatalogURL}
	cfgPath := filepath.Join(env.DataDir, "config.json")
	if b, err := os.ReadFile(cfgPath); err == nil {
		json.Unmarshal(b, &env.Config)
	} else {
		b, _ := json.MarshalIndent(env.Config, "", "  ")
		os.WriteFile(cfgPath, b, 0o644)
	}
	if env.Config.CatalogURL == "" {
		env.Config.CatalogURL = defaultCatalogURL
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConns = 4
	tr.ResponseHeaderTimeout = 25 * time.Second
	env.HTTP = &http.Client{Transport: tr}
	return env
}

func main() {
	shot := flag.String("shot", "", "render a scene to PNG instead of running on the device (boot|list|detail|busy|done|error)")
	out := flag.String("out", "shot.png", "PNG path for -shot")
	w := flag.Int("w", 640, "screen width for -shot")
	h := flag.Int("h", 480, "screen height for -shot")
	bench := flag.Int("bench", 0, "with -shot: render the scene N times and print the average frame time")
	flag.Parse()

	env := newEnv()
	if *shot != "" {
		c := NewCanvas(*w, *h)
		ui := NewUI(env, c.W, c.H)
		demoScene(ui, *shot)
		ui.Draw(c)
		if *bench > 0 {
			t0 := time.Now()
			for i := 0; i < *bench; i++ {
				ui.frame++
				ui.Draw(c)
			}
			fmt.Printf("%s: %.1f ms/frame\n", *shot, float64(time.Since(t0).Microseconds())/1000/float64(*bench))
		}
		f, err := os.Create(*out)
		if err != nil {
			log.Fatal(err)
		}
		png.Encode(f, c)
		f.Close()
		return
	}

	logf, _ := os.OpenFile(filepath.Join(env.DataDir, "mixtape.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if logf != nil {
		log.SetOutput(logf)
	}
	log.Printf("mixtape %s starting, sd=%s", version, env.SDRoot)

	fb, err := OpenFramebuffer()
	if err != nil {
		log.Fatalf("framebuffer: %v", err)
	}
	defer fb.Close()
	c := NewCanvas(fb.W, fb.H)
	keys := make(chan int, 32)
	if err := ReadInput("/dev/input/event0", keys); err != nil {
		log.Fatalf("input: %v", err)
	}
	ui := NewUI(env, c.W, c.H)
	ui.Start()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	lastMinute := -1
	dirty := true
	for !ui.quit {
		select {
		case k := <-keys:
			ui.Key(k)
			dirty = true
		case f := <-ui.post:
			f()
			dirty = true
		case <-tick.C:
			ui.frame++
			if ui.Animating() {
				dirty = true
			}
			if m := time.Now().Minute(); m != lastMinute {
				lastMinute = m
				dirty = true
			}
		}
		if dirty {
			ui.Draw(c)
			fb.Present(c)
			dirty = false
		}
	}
	fb.Clear()
	fmt.Fprintln(os.Stderr, "bye")
}
