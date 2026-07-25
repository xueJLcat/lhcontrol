package main

import (
	"fmt"
	"testing"

	"lhcontrol/internal/station"

	"github.com/gofiber/fiber/v2"
)

func TestAPIStatusForError(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{station.ErrInvalidArgument, fiber.StatusBadRequest},
		{station.ErrNotFound, fiber.StatusNotFound},
		{station.ErrOperationInProgress, fiber.StatusConflict},
		{station.ErrChannelConflict, fiber.StatusConflict},
		{station.ErrScanRequired, fiber.StatusConflict},
		{station.ErrUnsupported, fiber.StatusUnprocessableEntity},
		{fmt.Errorf("BLE failure"), fiber.StatusInternalServerError},
	}
	for _, test := range tests {
		if got := apiStatusForError(fmt.Errorf("wrapped: %w", test.err)); got != test.want {
			t.Errorf("apiStatusForError(%v) = %d, want %d", test.err, got, test.want)
		}
	}
}

func TestNewAppReportsConfiguredAPIAddress(t *testing.T) {
	app := NewApp()
	status := app.GetAPIStatus()
	if status.Running || status.Address != "127.0.0.1:7575" || status.Error != "" {
		t.Fatalf("initial API status = %+v", status)
	}
}
