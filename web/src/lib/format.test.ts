import { describe, expect, it } from "vitest";
import { formatBytes, formatDateTime, formatRate, percent, relativeTime, operationErrorText, isHistoricalFailure } from "./format";

describe("format helpers", () => {
  it("explains unsupported operations without misdiagnosing invalid parameters", () => {
    expect(operationErrorText("operation_invalid", "operation type is unsupported")).toContain("升级 Agent");
    expect(operationErrorText("operation_invalid", "invalid target")).not.toContain("升级");
    expect(operationErrorText("ping_failed", "exit status 1")).toBe("目标没有响应 Ping");
    expect(operationErrorText("future_error", "original reason")).toBe("original reason");
    expect(operationErrorText(undefined, "original reason")).toBe("original reason");
  });
  it("collapses only failed records older than seven days", () => {
    const now = Date.parse("2026-10-14T12:00:00Z");
    expect(isHistoricalFailure("failed", "2026-10-07T11:59:59Z", now)).toBe(true);
    expect(isHistoricalFailure("failed", "2026-10-07T12:00:00Z", now)).toBe(false);
    expect(isHistoricalFailure("succeeded", "2026-10-01T00:00:00Z", now)).toBe(false);
    expect(isHistoricalFailure("failed", "invalid", now)).toBe(false);
  });
  it("formats byte and network values", () => {
    expect(formatBytes(1024 ** 3)).toBe("1.0 GiB");
    expect(formatBytes(0)).toBe("0 B");
    expect(formatRate(1_500_000)).toBe("1.5 Mbps");
  });

  it("clamps percentages", () => {
    expect(percent(1, 4)).toBe(25);
    expect(percent(5, 4)).toBe(100);
    expect(percent(1, 0)).toBe(0);
  });

  it("renders relative heartbeat time", () => {
    const now = Date.parse("2026-08-07T12:00:00Z");
    expect(relativeTime("2026-08-07T11:59:30Z", now)).toBe("30 秒前");
    expect(relativeTime(null, now)).toBe("尚未连接");
  });

  it("renders empty and invalid expiry values safely", () => {
    expect(formatDateTime(null)).toBe("永不过期");
    expect(formatDateTime(null, false)).toBe("-");
    expect(formatDateTime("not-a-date")).toBe("时间未知");
  });
});
