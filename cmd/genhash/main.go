package main

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

func main() {
	passwords := map[string]string{
		"admin":   "ObsidianAdmin#2024",
		"analyst": "ObsidianAnalyst#2024",
		"viewer":  "ObsidianViewer#2024",
	}

	for user, pass := range passwords {
		hash, err := bcrypt.GenerateFromPassword([]byte(pass), 12)
		if err != nil {
			fmt.Printf("Error for %s: %v\n", user, err)
			continue
		}
		fmt.Printf("%s: %s\n", user, string(hash))
	}
}
