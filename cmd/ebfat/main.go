package main

import (
	"log"
	"math/rand"
	"os"
	"time"

	"github.com/YorikSar/ebfat/pkg/ebfat"
)

func main() {
	rand.Seed(time.Now().UTC().UnixNano())

	files := make([]ebfat.File, len(os.Args)-1)
	for i, fname := range os.Args[1:] {
		log.Printf("Opening %s", fname)
		f, err := os.Open(fname)
		if err != nil {
			log.Fatalf("Failed to open file %s: %s", fname, err)
		}
		stat, err := f.Stat()
		if err != nil {
			log.Fatalf("Failed to stat file %s: %s", fname, err)
		}
		files[i] = ebfat.File{
			Name:   stat.Name(),
			Size:   stat.Size(),
			Reader: f,
		}
	}
	err := ebfat.CreateFat(files, os.Stdout, "THELABEL")
	if err != nil {
		log.Fatalf("Failed to create FAT image: %v", err)
	}
}
