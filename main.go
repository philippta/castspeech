package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	cast "github.com/AndreasAbdi/gochromecast"
	"github.com/miekg/dns"
)

func main() {
	log.SetFlags(0)

	if len(os.Args) != 2 {
		log.Println("Usage: castaudio <file>")
		return
	}

	url, mime, err := HostFile(os.Args[1])
	if err != nil {
		panic(err)
	}

	ip, port, err := LookupDevice("_googlecast._tcp")
	if err != nil {
		panic(err)
	}

	PlaySound(ip, port, url, mime)
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

func PlaySound(ip net.IP, port int, url, mimetype string) error {
	dev, err := cast.NewDevice(ip, port)
	if err != nil {
		return err
	}

	dev.PlayMedia(url, mimetype)
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

	conn.SetReadDeadline(time.Now().Add(1 * time.Second))

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
