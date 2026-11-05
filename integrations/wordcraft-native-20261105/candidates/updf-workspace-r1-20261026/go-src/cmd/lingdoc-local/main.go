package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Tencent/WeKnora/internal/container"
	"github.com/gin-gonic/gin"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	gin.SetMode(gin.ReleaseMode)
	if os.Getenv("JWT_SECRET") == "" {
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return err
		}
		if err := os.Setenv("JWT_SECRET", hex.EncodeToString(secret)); err != nil {
			return err
		}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	address := os.Getenv("LINGDOC_LOCAL_ADDR")
	app, err := container.NewLocalResearchApplication(ctx, address, os.Getenv("LINGDOC_LOCAL_ORIGIN"), os.Getenv("LINGDOC_LOCAL_RUNTIME_ID"))
	if err != nil {
		return err
	}
	defer app.Cleaner.Cleanup(context.Background())
	server := &http.Server{Addr: address, Handler: app.Handler, ReadHeaderTimeout: container.LocalResearchReadHeaderTimeout}
	result := make(chan error, 1)
	go func() { result <- server.ListenAndServe() }()
	encoded, _ := json.Marshal(app.Bootstrap)
	fmt.Printf("LINGDOC_LOCAL_BOOTSTRAP=%s\n", encoded)
	select {
	case err = <-result:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	case <-app.Stop:
	}
	shutdown, release := context.WithTimeout(context.Background(), 10*time.Second)
	defer release()
	return server.Shutdown(shutdown)
}
