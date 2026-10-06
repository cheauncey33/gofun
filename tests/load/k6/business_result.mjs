export function classifyResponse(status, body) {
  if (status === 0) return "transport_error";
  if (status >= 500) return "server_error";
  if (status === 200 && body?.code === 0) return "accepted";
  if (status === 409 && body?.code === 60001) return "sold_out";
  if (status === 429 && body?.code === 42900) return "rate_limited";
  if ([400, 409].includes(status) && Number.isInteger(body?.code)) return "business_rejected";
  return "unexpected";
}
