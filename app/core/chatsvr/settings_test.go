package chatsvr

import "testing"

func TestSafetyCheckDefaultsOn(t *testing.T) {
	app, err := New(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if !app.Settings().SafetyCheck {
		t.Fatal("SafetyCheck should default to true on a fresh data dir")
	}
}

func TestSafetyCheckOffSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	app, err := New(Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.store.SetKV("safety_check", "0"); err != nil {
		t.Fatal(err)
	}
	app.Close()

	reopened, err := New(Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.Settings().SafetyCheck {
		t.Fatal("SafetyCheck should stay off once stored as 0")
	}
}

func TestSafetyCheckTogglesBackOn(t *testing.T) {
	dir := t.TempDir()
	app, err := New(Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.store.SetKV("safety_check", "0"); err != nil {
		t.Fatal(err)
	}
	app.loadSettings()
	if app.Settings().SafetyCheck {
		t.Fatal("SafetyCheck should be off after storing 0")
	}
	if err := app.store.SetKV("safety_check", "1"); err != nil {
		t.Fatal(err)
	}
	app.loadSettings()
	if !app.Settings().SafetyCheck {
		t.Fatal("SafetyCheck should be on after storing 1")
	}
	app.Close()
}
