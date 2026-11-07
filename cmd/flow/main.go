package main

import (
	"context"

	"github.com/unaquatic/flow/internal/cmdline"
)

func main() {
	cmdline.Main(context.Background(), rootCmd)
}
