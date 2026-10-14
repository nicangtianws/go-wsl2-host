//go:build windows
// +build windows

package internal

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
	"golang.org/x/term"
)

func exePath() (string, error) {
	prog := os.Args[0]
	p, err := filepath.Abs(prog)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(p)
	if err == nil {
		if !fi.Mode().IsDir() {
			return p, nil
		}
		err = fmt.Errorf("%s is directory", p)
	}
	if filepath.Ext(p) == "" {
		p += ".exe"
		fi, err := os.Stat(p)
		if err == nil {
			if !fi.Mode().IsDir() {
				return p, nil
			}
			err = fmt.Errorf("%s is directory", p)
		}
	}
	return "", err
}

// InstallService installs the Windows service and starts it
func InstallService(name, desc string) error {
	exepath, err := exePath()
	if err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err == nil {
		s.Close()
		return fmt.Errorf("service %s already exists", name)
	}
	fmt.Printf("Windows Username: ")
	var username string
	fmt.Scanln(&username)
	if !strings.Contains(username, "\\") && !strings.Contains(username, "@") {
		username = fmt.Sprintf(".\\%s", strings.TrimSpace(username))
	}
	fmt.Printf("Windows Password: ")
	bytePassword, err := term.ReadPassword(int(syscall.Stdin))
	if err != nil {
		return err
	}
	password := strings.TrimSpace(string(bytePassword))
	s, err = m.CreateService(name, exepath, mgr.Config{DisplayName: desc, StartType: mgr.StartAutomatic, ServiceStartName: username, Password: password}, "is", "auto-started")
	if err != nil {
		return err
	}
	defer s.Close()
	err = eventlog.InstallAsEventCreate(name, eventlog.Error|eventlog.Warning|eventlog.Info)
	if err != nil {
		s.Delete()
		return fmt.Errorf("SetupEventLogSource() failed: %s", err)
	}
	StartService(name)
	return nil
}

// RemoveService stops and uninstalls the Windows service
func RemoveService(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(name)
	switch {
	case err == nil:
		// The service must be stopped before it is deleted. Deleting a running
		// service only marks it for deletion (ERROR_SERVICE_MARKED_FOR_DELETE);
		// it stays running until it is stopped and only then is it removed.
		serr := stopService(s)
		if serr == nil {
			serr = s.Delete()
			if errors.Is(serr, windows.ERROR_SERVICE_MARKED_FOR_DELETE) {
				// A previous remove already marked the service for deletion;
				// waiting for it below is all that is left to do.
				serr = nil
			}
		}
		s.Close()
		if serr != nil {
			return fmt.Errorf("failed to delete service %s: %w", name, serr)
		}
	case errors.Is(err, windows.ERROR_SERVICE_MARKED_FOR_DELETE):
		// Already marked for deletion by a previous remove; wait for it below.
	case errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST):
		// Not installed; only a stale event log source may be left behind.
	case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return fmt.Errorf("could not open service %s: %w (run as administrator)", name, err)
	default:
		return fmt.Errorf("could not open service %s: %w", name, err)
	}

	// Windows removes a service marked for deletion asynchronously, once the
	// service has stopped and every handle to it has been closed. Wait for that
	// to finish so a subsequent install does not fail.
	if err := waitForRemoval(m, name); err != nil {
		return err
	}

	// Removing the event log source is a best effort operation: the source may
	// never have been created, or may already have been removed.
	if err := eventlog.Remove(name); err != nil &&
		!errors.Is(err, windows.ERROR_FILE_NOT_FOUND) &&
		!errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
		return fmt.Errorf("failed to remove event log source for %s: %w", name, err)
	}

	return nil
}
