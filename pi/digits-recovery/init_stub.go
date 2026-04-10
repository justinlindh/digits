//go:build !linux

package main

func initSetup() {}

func rebootDirect() error {
	return nil
}
