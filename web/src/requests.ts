// Only the latest request for the current screen may publish rows, cursors or
// errors. Invalidating also suppresses finally callbacks from previous screens.
export class LatestRequest {
  private generation = 0
  invalidate() {
    this.generation++
  }
  async run<T>(
    operation: () => Promise<T>,
    apply: (value: T) => void,
    fail: (error: unknown) => void,
    finish: () => void,
  ): Promise<void> {
    const generation = ++this.generation
    try {
      const value = await operation()
      if (generation === this.generation) apply(value)
    } catch (error) {
      if (generation === this.generation) fail(error)
    } finally {
      if (generation === this.generation) finish()
    }
  }
}
