//go:build windows
// +build windows

package internal

import (
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// StartService starts a Windows service given a name
func StartService(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		return fmt.Errorf("could not access service: %v", err)
	}
	defer s.Close()
	err = s.Start("is", "manual-started")
	if err != nil {
		return fmt.Errorf("could not start service: %v", err)
	}
	return nil
}

// stopService stops the given service, waiting until it has reached the stopped
// state. Stopping an already stopped service is not an error.
func stopService(s *mgr.Service) error {
	status, err := s.Query()
	if err != nil {
		return fmt.Errorf("could not query service %s: %w", s.Name, err)
	}
	if status.State != svc.Stopped && status.State != svc.StopPending {
		if _, err := s.Control(svc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
			return fmt.Errorf("could not stop service %s: %w", s.Name, err)
		}
	}
	timeout := time.Now().Add(30 * time.Second)
	for {
		status, err = s.Query()
		if err != nil {
			return fmt.Errorf("could not retrieve status for service %s: %w", s.Name, err)
		}
		if status.State == svc.Stopped {
			return nil
		}
		if timeout.Before(time.Now()) {
			return fmt.Errorf("timeout waiting for service %s to stop", s.Name)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// waitForRemoval blocks until the service is no longer present in the service
// control manager database. Windows only removes a service marked for deletion
// after it has stopped and every handle to it has been closed.
func waitForRemoval(m *mgr.Mgr, name string) error {
	timeout := time.Now().Add(30 * time.Second)
	for {
		s, err := m.OpenService(name)
		if err != nil {
			if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) ||
				errors.Is(err, windows.ERROR_SERVICE_MARKED_FOR_DELETE) {
				// The service is gone, or is already being removed.
				return nil
			}
			return fmt.Errorf("could not open service %s: %w", name, err)
		}
		s.Close()
		if timeout.Before(time.Now()) {
			return fmt.Errorf("timeout waiting for service %s to be removed; stop it and run remove again", name)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// ControlService used to stop/pause or continue Windows service
func ControlService(name string, c svc.Cmd, to svc.State) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		return fmt.Errorf("could not access service: %v", err)
	}
	defer s.Close()
	status, err := s.Control(c)
	if err != nil {
		return fmt.Errorf("could not send control=%d: %v", c, err)
	}
	timeout := time.Now().Add(10 * time.Second)
	for status.State != to {
		if timeout.Before(time.Now()) {
			return fmt.Errorf("timeout waiting for service to go to state=%d", to)
		}
		time.Sleep(300 * time.Millisecond)
		status, err = s.Query()
		if err != nil {
			return fmt.Errorf("could not retrieve service status: %v", err)
		}
	}
	return nil
}
