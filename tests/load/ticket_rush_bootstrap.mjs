import { bootstrapRushCampaign } from "../integration/lib/catalog_bootstrap.mjs";

const totalQuota = Number(process.env.RUSH_TOTAL_QUOTA || 100000);
const perUserLimit = Number(process.env.RUSH_PER_USER_LIMIT || 6);
const tierQuota = Number(process.env.RUSH_TIER_QUOTA || totalQuota);

const result = await bootstrapRushCampaign({
  totalQuota,
  perUserLimit,
  tierQuota,
  label: process.env.RUSH_LABEL || "load-rush",
});

console.log(JSON.stringify({
  campaign_id: result.campaignID,
  event_id: result.eventID,
  tier_id: result.tierID,
  organizer_id: result.organizerID,
  total_quota: result.totalQuota,
  per_user_limit: result.perUserLimit,
}, null, 2));
