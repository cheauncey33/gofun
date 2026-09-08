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
    await page.goto(baseURL, { waitUntil: 'domcontentloaded' })
    await waitReady(page, '.featured .slide')
    await sleep(900)
    const next = page.locator('.featured .nav.next')
    if (await next.count()) {
      await moveTo(page, next)
      await next.click()
      await sleep(800)
    }
    const grid = page.locator('.event-grid')
    await grid.scrollIntoViewIfNeeded()
    await sleep(1000)
    await shot(page, '01-home')

    await openAuthed(page, 'user', 'user123', `/events/${seatedEventId}`)
    await waitReady(page, '.detail-page h1')
    await sleep(900)
    const pick = page.locator('.primary-action')
    await moveTo(page, pick)
    await sleep(300)
    const pickLabel = (await pick.innerText()).trim()
    if (!pickLabel.includes('选座') || await pick.isDisabled()) {
      await shot(page, '02-detail')
      throw new Error(`选座按钮不可用：${pickLabel}`)
    }
    await pick.click()
    await waitReady(page, '.seat-map')
    const seat = page.locator('.seat-btn:not([disabled])').nth(3)
    if (await seat.count()) {
      await moveTo(page, seat)
      await seat.click()
    }
    await sleep(1400)
    await shot(page, '02-seats')
    const close = page.getByRole('button', { name: '关闭' })
    if (await close.count()) await close.click()

    await openAuthed(page, 'organizer', 'organizer123', '/organizer')
    await waitReady(page, '.sell-hero')
    await page.locator('.sell-hero').scrollIntoViewIfNeeded()
    await sleep(1100)
    const funnel = page.locator('.funnel-board')
    if (await funnel.count()) {
      await funnel.scrollIntoViewIfNeeded()
      await sleep(1100)
    }
    await shot(page, '03-organizer')

    await openAuthed(page, 'admin', 'admin123', '/admin')
    await waitReady(page, '.sre-grid')
    await page.locator('.sre-grid').scrollIntoViewIfNeeded()
    await sleep(1600)
    const pipeline = page.locator('.pipeline-list')
    if (await pipeline.count()) {
      await pipeline.scrollIntoViewIfNeeded()
      await sleep(900)
    }
    await shot(page, '04-admin')
    await sleep(500)
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
