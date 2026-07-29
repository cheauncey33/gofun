/**
 * 自举一条「可立刻抢」的限时开售：admin 建主办方 → owner 建目录 → 发布 → 建 campaign。
 * 返回 { campaignID, tierID, organizerID, owner }
 */
import { randomUUID } from "node:crypto";
import { assert, http, loginAdmin, purchaseBody, registerAndLogin } from "./http.mjs";

function iso(offsetMs) {
  return new Date(Date.now() + offsetMs).toISOString();
}

export async function bootstrapRushCampaign({
  totalQuota = 5,
  perUserLimit = 1,
  tierQuota = 20,
  label = "it-rush",
} = {}) {
  const suffix = `${Date.now().toString(36)}${randomUUID().slice(0, 4)}`;
  const owner = await registerAndLogin(`o${suffix}`.slice(0, 20));
  const adminToken = await loginAdmin();

  const orgRes = await http("POST", "/admin/organizers", {
    token: adminToken,
    body: {
      name: `IT抢票主办方-${suffix}`,
      slug: `it${suffix}`.toLowerCase().replace(/[^a-z0-9-]/g, "").slice(0, 32),
      contact_name: "IT",
      contact_phone: "13800000000",
      owner_user_id: owner.userID,
    },
  });
  assert(orgRes.ok && orgRes.data?.data?.id, `create organizer failed: ${JSON.stringify(orgRes.data)}`);
  const organizerID = String(orgRes.data.data.id);

  const venueRes = await http("POST", `/organizers/${organizerID}/venues`, {
    token: owner.token,
    body: {
      name: "IT 测试场馆",
      city: "武汉",
      district: "洪山区",
      address: "集成测试路 1 号",
      timezone: "Asia/Shanghai",
    },
  });
  assert(venueRes.ok && venueRes.data?.data?.id, `create venue failed: ${JSON.stringify(venueRes.data)}`);
  const venueID = String(venueRes.data.data.id);

  const eventRes = await http("POST", `/organizers/${organizerID}/events`, {
    token: owner.token,
    body: {
      title: `IT抢票验收-${suffix}`,
      subtitle: "并发套件专用",
      category: "测试",
      description: "integration rush concurrency",
      real_name_required: false,
      max_tickets_per_order: 6,
    },
  });
  assert(eventRes.ok && eventRes.data?.data?.id, `create event failed: ${JSON.stringify(eventRes.data)}`);
  const eventID = String(eventRes.data.data.id);

  const sessionRes = await http("POST", `/organizers/${organizerID}/events/${eventID}/sessions`, {
    token: owner.token,
    body: {
      venue_id: venueID,
      starts_at: iso(7 * 24 * 3600 * 1000),
      ends_at: iso(7 * 24 * 3600 * 1000 + 3 * 3600 * 1000),
      sale_starts_at: iso(-60_000),
      sale_ends_at: iso(6 * 24 * 3600 * 1000),
    },
  });
  assert(sessionRes.ok && sessionRes.data?.data?.id, `create session failed: ${JSON.stringify(sessionRes.data)}`);
  const sessionID = String(sessionRes.data.data.id);

  const tierRes = await http("POST", `/organizers/${organizerID}/sessions/${sessionID}/ticket-tiers`, {
    token: owner.token,
    body: {
      name: "IT票档",
      price_cents: 12800,
      total_quota: Math.max(tierQuota, totalQuota),
      purchase_limit: Math.max(perUserLimit, 2),
    },
  });
  assert(tierRes.ok && tierRes.data?.data?.id, `create tier failed: ${JSON.stringify(tierRes.data)}`);
  const tierID = String(tierRes.data.data.id);

  const publishRes = await http("POST", `/organizers/${organizerID}/events/${eventID}/publish`, {
    token: owner.token,
  });
  assert(publishRes.ok, `publish failed: ${JSON.stringify(publishRes.data)}`);

  const rushRes = await http("POST", `/organizers/${organizerID}/rush-sales`, {
    token: owner.token,
    body: {
      ticket_tier_id: tierID,
      name: `IT直抢-${suffix}`,
      rush_price_cents: 6800,
      total_quota: totalQuota,
      per_user_limit: perUserLimit,
      starts_at: iso(-60_000),
      ends_at: iso(2 * 3600 * 1000),
    },
  });
  assert(rushRes.ok && rushRes.data?.data?.id, `create rush failed: ${JSON.stringify(rushRes.data)}`);
  const campaignID = String(rushRes.data.data.id);

  return {
    campaignID,
    tierID,
    organizerID,
    eventID,
    owner,
    totalQuota,
    perUserLimit,
  };
}

export async function executeRush(token, campaignID, idempotencyKey, contactName = "并发用户") {
  return http("POST", `/rush-sales/${campaignID}/execute`, {
    token,
    headers: { "X-Idempotency-Key": idempotencyKey },
    body: purchaseBody(contactName),
  });
}
