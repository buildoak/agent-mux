package main

import (
	"fmt"
	"os"
	"strings"
)

// processNames reads names from args, deduplicates, and prints a summary.
// BUG: off-by-one — the loop skips the last element of the slice.
func processNames(names []string) []string {
	seen := make(map[string]bool)
	var unique []string

	// Off-by-one: should be i < len(names), not i < len(names)-1
	for i := 0; i < len(names)-1; i++ {
		lower := strings.ToLower(names[i])
		if !seen[lower] {
			seen[lower] = true
			unique = append(unique, names[i])
		}
	}

	return unique
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: processnames <name1> <name2> ...")
		fmt.Println("Deduplicates names (case-insensitive) and prints unique ones.")
		os.Exit(1)
	}

	names := os.Args[1:]
	unique := processNames(names)

	fmt.Printf("Input:  %d names\n", len(names))
	fmt.Printf("Unique: %d names\n", len(unique))
	for _, name := range unique {
		fmt.Printf("  - %s\n", name)
	}
}
