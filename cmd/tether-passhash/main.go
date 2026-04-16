package main

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/crypto/bcrypt"
)

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 {
		fmt.Fprintln(stderr, "usage: tether-passhash <password>")
		return 2
	}
	b, err := bcrypt.GenerateFromPassword([]byte(args[1]), bcrypt.DefaultCost)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}

func main() {
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}
