package javacheck

import (
	"context"
	"testing"
)

func TestParseMajor(t *testing.T) {
	cases := map[string]int{
		`openjdk version "21.0.1" 2023-10-17`:                 21,
		`java version "17.0.9" 2023-10-17 LTS`:                17,
		`openjdk version "1.8.0_392"`:                         8,
		`java version "21" 2023-09-19`:                        21,
		"Picked up JAVA_OPTS\nopenjdk version \"17.0.2\" ...": 17,
	}
	for out, want := range cases {
		got, err := parseMajor(out)
		if err != nil {
			t.Fatalf("parseMajor(%q): %v", out, err)
		}
		if got != want {
			t.Fatalf("parseMajor(%q) = %d, quiero %d", out, got, want)
		}
	}
	if _, err := parseMajor("sin version aqui"); err == nil {
		t.Fatal("salida irreconocible debe fallar")
	}
}

func TestRequired(t *testing.T) {
	cases := map[string]int{
		"1.16.5":  8,
		"1.12.2":  8,
		"1.17.1":  17,
		"1.20.1":  17,
		"1.20.4":  17,
		"1.20.5":  21,
		"1.21":    21,
		"1.21.11": 21,
		"26.2":    21,
		"26.1.1":  21,
		"":        0,
		"rara":    0,
	}
	for mc, want := range cases {
		if got := Required(mc); got != want {
			t.Fatalf("Required(%q) = %d, quiero %d", mc, got, want)
		}
	}
}

func TestVersionNonexistentJavaFails(t *testing.T) {
	if _, err := Version(context.Background(), "java-que-no-existe-xyz"); err == nil {
		t.Fatal("java inexistente debe fallar")
	}
}
