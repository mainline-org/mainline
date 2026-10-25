export class PluginError extends Error {
  constructor(code, message, detail = null) {
    super(message);
    this.code = code;
    this.detail = detail;
  }
}

export function errorRecord(error, stage) {
  return {
    code: error.code ?? "Failed",
    message: error.message ?? String(error),
    detail: error.detail ?? null,
    stage,
  };
}

export class OperationError extends Error {
  constructor(record) {
    super(record.message);
    this.record = record;
  }
}
