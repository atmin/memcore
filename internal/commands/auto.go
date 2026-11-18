package commands

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/atmin/memcore/internal/store"
)

// autoPushKey is the setting of the automatic push, in the git configuration of the memory repo.
const autoPushKey = "memcore.autopush"

// autoPushDelay reads the setting of the automatic push. An unset key means off. A value is the delay before a
// chain is ripe, as time.ParseDuration reads it, such as 24h. 0s means no delay.
func autoPushDelay(repo string) (delay time.Duration, on bool, err error) {
	out, err := store.Git(repo, "config", "--get", autoPushKey)
	if err != nil {
		// Only the exit status 1 of git means that the key is unset.
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("read the setting %s: %w", autoPushKey, err)
	}
	value := strings.TrimSpace(out)
	delay, err = time.ParseDuration(value)
	if err != nil {
		return 0, false, fmt.Errorf("read the setting %s: the value %q is no duration, such as 24h: %w", autoPushKey, value, err)
	}
	if delay < 0 {
		return 0, false, fmt.Errorf("read the setting %s: the value %q is negative", autoPushKey, value)
	}
	return delay, true, nil
}
