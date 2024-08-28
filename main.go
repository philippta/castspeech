package main

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	cast "github.com/AndreasAbdi/gochromecast"
	"github.com/miekg/dns"
)

func main() {
	log.SetFlags(0)

	if len(os.Args) < 2 {
		log.Println("Usage: castaudio <text> [language]")
		return
	}

	text := os.Args[1]
	lang := "de"

	if len(os.Args) == 3 {
		lang = os.Args[2]
	}

	audio, ok := lookupAudio(text, lang)
	if !ok {
		var err error
		audio, err = TTS(text, lang)
		if err != nil {
			log.Fatal(err)
		}
		saveAudio(text, lang, audio)
	}

	url, mime, err := HostAudio(audio)
	if err != nil {
		log.Fatal(err)
	}

	ip, port, err := LookupDevice("_googlecast._tcp")
	if err != nil {
		panic(err)
	}

	PlaySound(ip, port, url, mime)
}

func lookupAudio(text, lang string) ([]byte, bool) {
	file, err := audioFilepath(text, lang)
	if err != nil {
		return nil, false
	}

	audio, err := os.ReadFile(file)
	if err != nil {
		return nil, false
	}

	return audio, true
}

func saveAudio(text, lang string, audio []byte) {
	file, err := audioFilepath(text, lang)
	if err != nil {
		return
	}
	os.WriteFile(file, audio, 0o644)
}

func audioFilepath(text, lang string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	path := filepath.Join(home, ".castaudio")
	os.MkdirAll(path, 0755)

	hashb := md5.Sum([]byte(text))
	hash := hex.EncodeToString(hashb[:])
	file := filepath.Join(path, lang+"_"+hash)

	return file, nil
}

func HostFile(file string) (string, string, error) {
	buf, err := os.ReadFile(file)
	if err != nil {
		return "", "", err
	}

	mimeType := http.DetectContentType(buf)

	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		return "", "", err
	}

	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, file)
	}))

	var (
		port = ln.Addr().(*net.TCPAddr).Port
		ip   = GetOutboundIP()
		base = filepath.Base(file)
		url  = fmt.Sprintf("http://%s:%d/%s", ip, port, base)
	)

	return url, mimeType, nil
}

func HostAudio(audio []byte) (string, string, error) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		return "", "", err
	}

	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(audio)
	}))

	var (
		port     = ln.Addr().(*net.TCPAddr).Port
		ip       = GetOutboundIP()
		url      = fmt.Sprintf("http://%s:%d/audio", ip, port)
		mimeType = http.DetectContentType(audio)
	)

	return url, mimeType, nil
}

func PlaySound(ip net.IP, port int, url, mimetype string) error {
	dev, err := cast.NewDevice(ip, port)
	if err != nil {
		return err
	}

	appID := "CC1AD845"
	dev.ReceiverController.LaunchApplication(&appID, time.Second, false)
	dev.MediaController.Load(url, mimetype, time.Second)
	return nil
}

func LookupDevice(serviceAddr string) (net.IP, int, error) {
	var m dns.Msg
	m.SetQuestion(serviceAddr+".local.", dns.TypePTR)

	buf, err := m.Pack()
	if err != nil {
		return net.IP{}, 0, err
	}

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return net.IP{}, 0, err
	}

	conn.WriteToUDP(buf, &net.UDPAddr{
		IP:   net.ParseIP("224.0.0.251"),
		Port: 5353,
	})

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	resp := make([]byte, 65536)
	n, err := conn.Read(resp)
	if err != nil {
		return net.IP{}, 0, err
	}

	msg := new(dns.Msg)
	if err := msg.Unpack(resp[:n]); err != nil {
		return net.IP{}, 0, err
	}

	var port int
	var ip net.IP
	for _, extra := range msg.Extra {
		switch v := extra.(type) {
		case *dns.SRV:
			port = int(v.Port)
		case *dns.A:
			ip = v.A
		}
	}

	return ip, port, nil
}

func GetOutboundIP() net.IP {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)

	return localAddr.IP
}

func TTS(text, lang string) ([]byte, error) {
	encoded := strings.ReplaceAll(url.QueryEscape(text), "%", "%25")

	resp, err := http.Get(fmt.Sprintf("https://www.google.com/async/translate_tts?ttsp=tl:%s,txt:%s,spd:1.1&async=_fmt:jspb", lang, encoded))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, resp.Body); err != nil {
		return nil, err
	}

	segs := bytes.Split(buf.Bytes(), []byte("\n"))
	if len(segs) != 2 {
		return nil, fmt.Errorf("unexpected response")
	}

	var out struct {
		TranslateTTS []string `json:"translate_tts"`
	}
	if err := json.Unmarshal(segs[1], &out); err != nil {
		return nil, err
	}

	audio, err := base64.StdEncoding.DecodeString(out.TranslateTTS[0])
	if err != nil {
		return nil, err
	}

	return audio, nil
}

func writeTempFile(data []byte) (string, func(), error) {
	tempFile, err := os.CreateTemp(os.TempDir(), "example")
	if err != nil {
		return "", nil, err
	}
	tempFile.Write(data)
	cleanup := func() {
		tempFile.Close()
		os.Remove(tempFile.Name())
	}
	return tempFile.Name(), cleanup, nil
}
