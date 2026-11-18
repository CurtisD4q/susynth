module github.com/susynth/susynth

go 1.25.0

require github.com/ebitengine/oto/v3 v3.5.1

require (
	github.com/ebitengine/purego v0.11.0 // indirect
	github.com/jfreymuth/pulse v0.1.3 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace golang.org/x/sys => ./third_party/sys
