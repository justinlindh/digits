//go:build !linux

package main

func initSetup() {}

func closeDataLog() {}

func rebootDirect() error {
	return nil
}
