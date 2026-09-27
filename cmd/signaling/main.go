package main

import (
	"log"
	"net/http"
	"os"

	server "github.com/eirahoutmoss/remotesupport/server/signaling"
)

func main() {
	addr := os.Getenv("REMOTESUPPORT_SIGNALING_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8091"
	}

	s := server.NewServer()

	mux := s.Handler()

	log.Printf("RemoteSupport signaling listening on http://%s", addr)
	log.Printf("WebSocket endpoint: ws://%s/v1/ws", addr)

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
