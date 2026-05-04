module github.com/justinlindh/digits/pi/digits-setup

go 1.26

require github.com/justinlindh/digits/pi/phonekit v0.0.0

require golang.org/x/sys v0.43.0 // indirect

replace github.com/justinlindh/digits/pi/phonekit => ../phonekit
