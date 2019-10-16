package main

import "math/rand"

const letters = "abcdefghijklmnopqrstuvwxyz"

// RandStringBytes create string given n
// here is an amazing post, have no time to improve further or tldr.
// https://stackoverflow.com/questions/22892120/how-to-generate-a-random-string-of-a-fixed-length-in-go
func RandStringBytes(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}
