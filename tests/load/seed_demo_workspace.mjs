import { spawnSync } from 'node:child_process'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const BASE = process.env.BASE_URL || 'http://127.0.0.1:8080/api/v1'
const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')

async function req(method, path, { token, body, headers } = {}) {
  const res = await fetch(`${BASE}${path}`, {
    method,
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: token } : {}),
      ...headers,
    },
    body: body ? JSON.stringify(body) : undefined,
  })
  const json = await res.json().catch(() => ({}))
  if (!res.ok || json.code) {
    throw new Error(`${method} ${path} ${res.status} ${json.msg || JSON.stringify(json)}`)
  }
  return json.data
}

async function register(username, password) {
  const res = await fetch(`${BASE}/register`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  })
  const json = await res.json().catch(() => ({}))
  if (res.ok && json.code === 0) return
  if (json.code === 40901 || String(json.msg || '').includes('已存在')) return
  throw new Error(`register ${username}: ${json.msg || res.status}`)
}

async function login(username, password) {
  const data = await req('POST', '/login', { body: { username, password } })
  return data.access_token
}

async function waitOrderPayable(token, orderId) {
  for (let i = 0; i < 20; i++) {
    const order = await req('GET', `/orders/${orderId}`, { token })
    if (order.status === 'pending_payment') return order
    if (order.status === 'failed') throw new Error(`order ${orderId} failed`)
    await new Promise(r => setTimeout(r, 400))
  }
  throw new Error(`order ${orderId} still not pending_payment`)
}

async function ensureEvent(token, organizerId, spec) {
  const events = await req('GET', `/organizers/${organizerId}/events?page=1&page_size=50`, { token })
  const list = events.list || []
  const existing = list.find(item => item.title === spec.title)
  if (existing) return existing

  const event = await req('POST', `/organizers/${organizerId}/events`, {
    token,
    body: {
      title: spec.title,
      subtitle: spec.subtitle,
      category: spec.category,
      cover_url: spec.cover,
      description: spec.description,
      max_tickets_per_order: 6,
      real_name_required: false,
      sale_mode: 'counter',
    },
  })
  const session = await req('POST', `/organizers/${organizerId}/events/${event.id}/sessions`, {
    token,
    body: spec.session,
  })
  await req('POST', `/organizers/${organizerId}/sessions/${session.id}/ticket-tiers`, {
    token,
    body: {
      name: spec.tierName,
      description: spec.tierDesc,
      price_cents: spec.priceCents,
      total_quota: spec.quota,
      purchase_limit: 4,
    },
  })
  return event
}

async function main() {
  const password = 'gofun123456'
  await register('livehouse01', password)
  await register('pendingorg01', password)
  await register('buyer01', password)
  await register('buyer02', password)
  await register('buyer03', password)

  const admin = await login('admin', 'admin123')
  const organizer = await login('livehouse01', password)
  const pendingUser = await login('pendingorg01', password)
  const buyers = [
    await login('buyer01', password),
    await login('buyer02', password),
    await login('buyer03', password),
  ]

  const mine = await req('GET', '/organizers/mine', { token: organizer })
  let org = (mine || []).find(item => item.organizer?.slug === 'jiangcheng-live')?.organizer
  if (!org) {
    org = await req('POST', '/organizers/apply', {
      token: organizer,
      body: {
        name: '江城现场',
        slug: 'jiangcheng-live',
        contact_name: '陈晓',
        contact_phone: '13800138001',
        description: '武汉 Livehouse 主办方演示账号',
      },
    })
  }
  if (org.audit_status === 'pending') {
    await req('POST', `/admin/organizers/${org.id}/approve`, { token: admin })
    org.audit_status = 'approved'
  }

  const pendingMine = await req('GET', '/organizers/mine', { token: pendingUser })
  if (!(pendingMine || []).some(item => item.organizer?.slug === 'pending-comedy')) {
    await req('POST', '/organizers/apply', {
      token: pendingUser,
      body: {
        name: '江岸喜剧社',
        slug: 'pending-comedy',
        contact_name: '刘洋',
        contact_phone: '13900139002',
        description: '待审核的主办方申请，给管理员看审批列表',
      },
    })
  }

  const now = Date.now()
  const starts = new Date(now + 12 * 24 * 3600 * 1000)
  const ends = new Date(starts.getTime() + 3 * 3600 * 1000)
  const saleStarts = new Date(now - 24 * 3600 * 1000)
  const saleEnds = new Date(starts.getTime() - 2 * 3600 * 1000)
  const session = {
    venue_id: '',
    starts_at: starts.toISOString(),
    ends_at: ends.toISOString(),
    sale_starts_at: saleStarts.toISOString(),
    sale_ends_at: saleEnds.toISOString(),
  }

  const venues = await req('GET', `/organizers/${org.id}/venues`, { token: organizer })
  let venue = (venues || []).find(item => item.name === '武汉光谷青年剧场')
  if (!venue) {
    venue = await req('POST', `/organizers/${org.id}/venues`, {
      token: organizer,
      body: {
        name: '武汉光谷青年剧场',
        city: '武汉',
        district: '洪山区',
        address: '光谷步行街旁',
        timezone: 'Asia/Shanghai',
      },
    })
  }
  session.venue_id = String(venue.id)

  const live = await ensureEvent(organizer, org.id, {
    title: '夏夜回声 Livehouse 专场',
    subtitle: '独立乐队联合巡演武汉站',
    category: '音乐现场',
    cover: 'https://images.unsplash.com/photo-1470229722913-7c0e2dbbafd3?w=800&q=80',
    description: '三组独立乐队连开，适合想近距离看现场的观众。演示数据。',
    session,
    tierName: '内场站票',
    tierDesc: '舞台前方站区',
    priceCents: 18800,
    quota: 200,
  })
  const pendingEvent = await ensureEvent(organizer, org.id, {
    title: '秋日新专场（待审核）',
    subtitle: '给管理员看的上架申请',
    category: '音乐现场',
    cover: 'https://images.unsplash.com/photo-1501281668745-f7f57925c3b4?w=800&q=80',
    description: '已提交审核，尚未上架。',
    session,
    tierName: '早鸟票',
    tierDesc: '演示待审票档',
    priceCents: 12800,
    quota: 80,
  })

  if (live.status === 'draft') {
    await req('POST', `/organizers/${org.id}/events/${live.id}/submit-review`, { token: organizer })
    await req('POST', `/admin/events/${live.id}/approve`, { token: admin })
  } else if (live.status === 'pending_review') {
    await req('POST', `/admin/events/${live.id}/approve`, { token: admin })
  }
  if (pendingEvent.status === 'published') {
    await req('POST', `/organizers/${org.id}/events/${pendingEvent.id}/unpublish`, { token: organizer })
    pendingEvent.status = 'draft'
  }
  if (pendingEvent.status === 'draft') {
    await req('POST', `/organizers/${org.id}/events/${pendingEvent.id}/submit-review`, { token: organizer })
  }

  const published = await req('GET', `/events/${live.id}`)
  const tier = published.sessions?.[0]?.ticket_tiers?.[0]
  if (!tier) throw new Error('published event has no tier')

  const seeded = spawnSync('go', ['run', './cmd/seed_funnel', '-config', './config/config.yaml'], {
    cwd: path.join(ROOT, 'backend'),
    stdio: 'inherit',
  })
  if (seeded.status !== 0) {
    throw new Error(`seed_funnel exited ${seeded.status}`)
  }

  const paidIds = []
  for (let i = 0; i < 3; i++) {
    const receipt = await req('POST', '/orders', {
      token: buyers[i],
      headers: { 'X-Idempotency-Key': `demo-paid-${i}-${Date.now()}` },
      body: {
        ticket_tier_id: String(tier.id),
        quantity: 1,
        contact_name: `购票人${i + 1}`,
        contact_phone: `1380013800${i + 1}`,
        terms_accepted: true,
      },
    })
    await waitOrderPayable(buyers[i], receipt.order_id)
    await req('POST', `/orders/${receipt.order_id}/pay`, { token: buyers[i], body: { scenario: 'success' } })
    paidIds.push(receipt.order_id)
  }
  const unpaid = await req('POST', '/orders', {
    token: buyers[0],
    headers: { 'X-Idempotency-Key': `demo-unpaid-${Date.now()}` },
    body: {
      ticket_tier_id: String(tier.id),
      quantity: 1,
      contact_name: '待支付观众',
      contact_phone: '13800138999',
      terms_accepted: true,
    },
  })
  await waitOrderPayable(buyers[0], unpaid.order_id)

  await new Promise(r => setTimeout(r, 1200))
  console.log(JSON.stringify({
    organizer: 'livehouse01 / gofun123456',
    pendingOrganizer: 'pendingorg01 / gofun123456',
    buyers: 'buyer01-03 / gofun123456',
    admin: 'admin / admin123',
    publishedEvent: live.title,
    pendingEvent: pendingEvent.title,
    paidOrders: paidIds,
    unpaidOrder: unpaid.order_id,
  }, null, 2))
}

main().catch(err => {
  console.error(err)
  process.exit(1)
})
