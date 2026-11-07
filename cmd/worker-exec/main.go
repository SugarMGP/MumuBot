package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"mumu-bot/internal/workerexec"
)

func main() {
	listen := strings.TrimSpace(os.Getenv("MUMU_WORKER_LISTEN"))
	if listen == "" {
		listen = ":7469"
	}
	user := strings.TrimSpace(os.Getenv("MUMU_WORKER_USER"))
	home := strings.TrimSpace(os.Getenv("MUMU_WORKER_HOME"))
	shell := strings.TrimSpace(os.Getenv("MUMU_WORKER_SHELL"))
	server, err := workerexec.New(user, home, shell)
	if err != nil {
		fmt.Fprintf(os.Stderr, "worker executor config invalid: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("worker executor listening on %s (user=%s home=%s)\n", listen, server.DefaultUser, server.DefaultHome)
	if err := http.ListenAndServe(listen, server.Handler()); err != nil {
		fmt.Fprintf(os.Stderr, "worker executor failed: %v\n", err)
		os.Exit(1)
	}
}
