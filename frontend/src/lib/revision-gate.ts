export class RevisionGate {
  private revision = 0;
  private disposed = false;

  next(): number {
    return ++this.revision;
  }

  isCurrent(revision: number): boolean {
    return !this.disposed && revision === this.revision;
  }

  dispose(): void {
    this.disposed = true;
    this.revision++;
  }
}
