import { chromium } from 'playwright'
import { mkdir, rm } from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = join(dirname(fileURLToPath(import.meta.url)), '..')
const outDir = join(root, 'docs', 'demo')
const workDir = join(outDir, '.tmp')
const baseURL = process.env.DEMO_BASE_URL || 'http://127.0.0.1:5173'
const seatedEventId = process.env.DEMO_SEATED_EVENT_ID || '2'

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

async function waitReady(page, selector, timeout = 15000) {
  await page.locator(selector).first().waitFor({ state: 'visible', timeout })
  await page.locator('.el-loading-mask').first().waitFor({ state: 'hidden', timeout: 3000 }).catch(() => {})
}

async function moveTo(page, locator) {
  const box = await locator.boundingBox()
  if (!box) return
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2, { steps: 10 })
}

async function shot(page, name) {
  await page.screenshot({ path: join(workDir, `${name}.png`) })
}

async function titleCard(page, title, line = '', ms = 2000) {
  const html = `<!doctype html>
<html lang="zh-CN">
<head><meta charset="utf-8"></head>
<body style="margin:0;background:#f6f1ea;display:flex;flex-direction:column;align-items:center;justify-content:center;height:100vh;font-family:'Source Han Serif SC','Songti SC',Georgia,'Times New Roman',serif;color:#1c1917">
  <div style="font-size:52px;font-weight:600;letter-spacing:.16em">${title}</div>
  ${line ? `<div style="margin-top:18px;font-size:18px;color:#7a736c;letter-spacing:.18em">${line}</div>` : ''}
</body>
</html>`
  await page.goto(`data:text/html;charset=utf-8,${encodeURIComponent(html)}`)
  await sleep(ms)
}

async function openAuthed(page, username, password, path) {
  if (!page.url().startsWith(baseURL)) {
    await page.goto(baseURL, { waitUntil: 'domcontentloaded' })
  }
  const ok = await page.evaluate(async ({ username, password }) => {
    localStorage.clear()
    const res = await fetch('/api/v1/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password }),
    })
    const json = await res.json()
    const data = json.data || {}
    if (!data.access_token) return json.msg || 'login failed'
    localStorage.setItem('access_token', data.access_token)
    localStorage.setItem('token', data.access_token)
    if (data.refresh_token) localStorage.setItem('refresh_token', data.refresh_token)
    localStorage.setItem('username', data.username || username)
    localStorage.setItem('role', data.role || '')
    return ''
  }, { username, password })
  if (ok) throw new Error(`${username} 登录失败：${ok}`)
  await page.goto(`${baseURL}${path}`, { waitUntil: 'domcontentloaded' })
}

async function record() {
  await rm(workDir, { recursive: true, force: true })
  await mkdir(workDir, { recursive: true })

  const browser = await chromium.launch({ headless: true })
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 1,
    locale: 'zh-CN',
    recordVideo: { dir: workDir, size: { width: 1440, height: 900 } },
  })
  const page = await context.newPage()
  let video
  await page.addInitScript(() => {
    const draw = () => {
      if (document.getElementById('demo-cursor')) return
      const el = document.createElement('div')
      el.id = 'demo-cursor'
      el.style.cssText = 'position:fixed;z-index:2147483647;width:16px;height:16px;border:2px solid #111;border-radius:50%;background:#fff;pointer-events:none;transform:translate(-50%,-50%);box-shadow:0 3px 10px rgba(0,0,0,.28)'
      document.documentElement.appendChild(el)
      window.addEventListener('mousemove', (event) => {
        el.style.left = `${event.clientX}px`
        el.style.top = `${event.clientY}px`
      }, { passive: true })
    }
    document.addEventListener('DOMContentLoaded', draw)
    if (document.readyState !== 'loading') draw()
  })

  try {
    await titleCard(page, 'Gofun', '多主办方活动票务', 2200)

    await titleCard(page, '购票站', '发现活动', 1800)
    await page.goto(baseURL, { waitUntil: 'domcontentloaded' })
    await waitReady(page, '.featured .slide')
    await sleep(1100)
    const next = page.locator('.featured .nav.next')
    if (await next.count()) {
      await moveTo(page, next)
      await next.click()
      await sleep(900)
    }
    const rushCard = page.locator('.rush-card').first()
    if (await rushCard.count()) {
      await rushCard.scrollIntoViewIfNeeded()
      await moveTo(page, rushCard)
      await sleep(1400)
    }
    const grid = page.locator('.event-grid')
    await grid.scrollIntoViewIfNeeded()
    await sleep(1400)
    await shot(page, '01-home')

    await titleCard(page, '限时开售', '抢票中', 1800)
    await openAuthed(page, 'user', 'user123', '/rush-sales')
    await waitReady(page, '.rush-ticket, .rush-state')
    await sleep(1400)
    const liveRush = page.locator('.rush-ticket.live, .rush-ticket').first()
    if (await liveRush.count()) {
      await moveTo(page, liveRush)
      const buy = liveRush.getByRole('button', { name: '立即抢票' })
      if (await buy.count()) {
        await buy.click()
        await waitReady(page, '.rush-panel, .rush-modal')
        await sleep(1600)
        const cancel = page.locator('.rush-panel .ghost, .rush-modal .ghost')
        if (await cancel.count()) await cancel.first().click()
        else await page.keyboard.press('Escape')
        await sleep(400)
      } else {
        await sleep(1200)
      }
    }
    await shot(page, '02-rush')

    await titleCard(page, '选座购票', 'VIP / 前排 / 普通座', 1800)
    await page.goto(`${baseURL}/events/${seatedEventId}`, { waitUntil: 'domcontentloaded' })
    await waitReady(page, '.detail-page h1')
    await sleep(1200)
    const pick = page.locator('.primary-action')
    await moveTo(page, pick)
    await sleep(400)
    const pickLabel = (await pick.innerText()).trim()
    if (!pickLabel.includes('选座') || await pick.isDisabled()) {
      await shot(page, '03-detail')
      throw new Error(`选座按钮不可用：${pickLabel}`)
    }
    await pick.click()
    await waitReady(page, '.seat-map')
    await sleep(1600)
    const vipSeat = page.locator('.seat-btn[title="A4"], .seat-btn[title="A5"], .seat-btn:not([disabled])').first()
    if (await vipSeat.count()) {
      await moveTo(page, vipSeat)
      await vipSeat.click()
    }
    await sleep(1600)
    await shot(page, '03-seats')
    const close = page.getByRole('button', { name: '关闭' })
    if (await close.count()) await close.click()
    await sleep(500)

    await titleCard(page, '主办方', '销售、转化、厅图', 1800)
    await openAuthed(page, 'organizer', 'organizer123', '/organizer')
    await waitReady(page, '.sell-hero')
    await page.locator('.sell-hero').scrollIntoViewIfNeeded()
    await sleep(1600)
    const funnel = page.locator('.funnel-board')
    if (await funnel.count()) {
      await funnel.scrollIntoViewIfNeeded()
      await sleep(1400)
    }
    const hallsBtn = page.getByRole('button', { name: '厅图资产' })
    if (await hallsBtn.count()) {
      await moveTo(page, hallsBtn)
      await hallsBtn.click()
      await waitReady(page, '.hall-card, .empty, .el-dialog')
      if (await page.locator('.hall-card').count()) {
        await sleep(1800)
        await shot(page, '04-halls')
      }
      await page.keyboard.press('Escape')
      await sleep(400)
    }
    await shot(page, '04-organizer')

    await titleCard(page, '平台管理', '健康与审批', 1800)
    await openAuthed(page, 'admin', 'admin123', '/admin')
    await waitReady(page, '.sre-grid')
    await page.locator('.sre-grid').scrollIntoViewIfNeeded()
    await sleep(2000)
    const pipeline = page.locator('.pipeline-list')
    if (await pipeline.count()) {
      await pipeline.scrollIntoViewIfNeeded()
      await sleep(1200)
    }
    await shot(page, '04-admin')
    await sleep(800)
  } catch (error) {
    await shot(page, 'fail').catch(() => {})
    throw error
  } finally {
    video = page.video()
    await context.close().catch(() => {})
    await browser.close().catch(() => {})
  }
  if (!video) throw new Error('没有录到视频')
  return video.path()
}

const videoPath = await record()
console.log(videoPath)
