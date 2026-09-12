// 只有当前页面的最新请求可以更新列表、游标或错误信息。
// 使请求失效时，也会阻止旧页面请求的 finally 回调更新状态。
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
