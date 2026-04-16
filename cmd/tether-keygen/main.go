package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
)

func run(stdout, stderr io.Writer) int {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	fmt.Fprintln(stdout, base64.StdEncoding.EncodeToString(key))
	return 0
}

func main() {
	os.Exit(run(os.Stdout, os.Stderr))
}
