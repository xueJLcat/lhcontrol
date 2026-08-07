package main

import (
	"testing"
)

func TestBluetoothAdapterSettingsBindings(t *testing.T) {

	app := NewApp()

	adapters, err := app.ListBluetoothAdapters()

	if err != nil {

		t.Fatalf("ListBluetoothAdapters() error = %v", err)

	}

	if adapters == nil {

		t.Fatal("ListBluetoothAdapters() returned a nil slice")

	}

	// The test machine decides how many radios exist; only the shape is

	// asserted here, so the test stays valid with or without hardware.

	for _, adapter := range adapters {

		if adapter.DeviceID == "" {

			t.Fatalf("adapter %q has an empty device id", adapter.Name)

		}

	}

}

func TestAutoSleepSettingsBindings(t *testing.T) {

	t.Setenv("AppData", t.TempDir())

	app := NewApp()

	defaults := app.GetAutoSleepSettings()

	if defaults.Enabled {

		t.Fatal("fresh auto-sleep settings must be disabled by default")

	}

	invalid := defaults

	invalid.Target = "chrome"

	if err := app.SetAutoSleepSettings(invalid); err == nil {

		t.Fatal("SetAutoSleepSettings() accepted an invalid target")

	}

	invalid = defaults

	invalid.DelaySeconds = 5

	if err := app.SetAutoSleepSettings(invalid); err == nil {

		t.Fatal("SetAutoSleepSettings() accepted a below-minimum delay")

	}

	enabled := defaults

	enabled.Enabled = true

	enabled.Target = "steam"

	enabled.DelaySeconds = 120

	if err := app.SetAutoSleepSettings(enabled); err != nil {

		t.Fatalf("SetAutoSleepSettings() error = %v", err)

	}

	defer app.stopAutoSleep()

	restarted := NewApp()

	if err := restarted.config.Load(); err != nil {

		t.Fatalf("config.Load() error = %v", err)

	}

	got := restarted.GetAutoSleepSettings()

	if !got.Enabled || got.Target != "steam" || got.DelaySeconds != 120 {

		t.Fatalf("auto-sleep settings after simulated restart = %+v", got)

	}

}

func TestLanguageBindingsPersistAndValidate(t *testing.T) {

	t.Setenv("AppData", t.TempDir())

	app := NewApp()

	if got := app.GetLanguage(); got != "" {

		t.Fatalf("fresh language = %q, want system default marker", got)

	}

	if err := app.SetLanguage("fr"); err == nil {

		t.Fatal("SetLanguage() accepted unsupported language")

	}

	if err := app.SetLanguage("zh-CN"); err != nil {

		t.Fatalf("SetLanguage() error = %v", err)

	}

	if err := app.SetLanguage(""); err != nil {

		t.Fatalf("SetLanguage(system) error = %v", err)

	}

	if err := app.SetLanguage("zh-CN"); err != nil {

		t.Fatalf("SetLanguage() second error = %v", err)

	}

	restarted := NewApp()

	if err := restarted.config.Load(); err != nil {

		t.Fatalf("config.Load() error = %v", err)

	}

	if got := restarted.GetLanguage(); got != "zh-CN" {

		t.Fatalf("language after simulated restart = %q, want zh-CN", got)

	}

}
