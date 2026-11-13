// Restored mode (D-303) for every screen. App keeps it current from
// GET /restore and the events; a screen that starts a write the server
// refuses in restored mode disables it and shows RESTORED_WAIT (review L2
// of #227), like "Back up now".
export const restored = $state({ mode: false });

export const RESTORED_WAIT =
  "Restored mode: this waits until an admin confirms in Settings → Backups that the old controller is off.";
