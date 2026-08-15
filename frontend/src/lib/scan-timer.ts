// Owns the wall-clock timer behind the scan elapsed display. The elapsed
// seconds are pushed through a callback so the component keeps a plain
// reactive variable and the timer itself never touches UI state directly.
export class ScanTimer {
  private timer: ReturnType<typeof setInterval> | null = null;
  private startedAt: number | null = null;

  constructor(private readonly onElapsed: (seconds: number) => void) {}

  begin() {
    if (this.timer) {
      // A handover (for example an adopted external scan following a local
      // scan) re-arms the timer for the new scan; restart the clock instead
      // of accumulating the previous scan's elapsed time.
      this.startedAt = Date.now();
      this.onElapsed(0);
      return;
    }
    this.startedAt = Date.now();
    this.onElapsed(0);
    this.timer = setInterval(() => {
      if (this.startedAt !== null) {
        // Clamp against a backwards wall-clock adjustment so the elapsed
        // display never goes negative or freezes on a skewed clock.
        this.onElapsed(Math.max(0, Math.floor((Date.now() - this.startedAt) / 1000)));
      }
    }, 1000);
  }

  end() {
    if (this.timer) {
      clearInterval(this.timer);
      this.timer = null;
    }
    this.startedAt = null;
    this.onElapsed(0);
  }

  dispose() {
    this.end();
  }
}
