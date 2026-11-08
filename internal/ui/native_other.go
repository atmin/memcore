//go:build (!darwin && !linux) || nonative

package ui

// RunHost runs serve with Window, because this build has no native window: a build with -tags nonative, or a
// system that glaze does not serve here.
func RunHost(serve func(window func(url string) (stop func(), err error), quit, reopen <-chan struct{}) error) error {
	return serve(Window, nil, nil)
}
