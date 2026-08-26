<script setup>
import QrcodeVue from 'qrcode.vue'

defineProps({
  ticket: { type: Object, required: true },
  attendee: { type: Object, default: null },
})

const ticketStatus = {
  valid: { label: '可入场', tone: 'valid' },
  used: { label: '已核销', tone: 'used' },
  revoked: { label: '已作废', tone: 'revoked' },
}

const dateTime = value => value ? new Intl.DateTimeFormat('zh-CN', {
  year: 'numeric', month: 'long', day: 'numeric', hour: '2-digit', minute: '2-digit',
}).format(new Date(value)) : '—'
</script>

<template>
  <article class="electronic-ticket" :class="ticketStatus[ticket.status]?.tone">
    <div class="ticket-copy">
      <span>Gofun 电子票 {{ ticket.sequence_no }}</span>
      <h3>{{ ticket.order_item?.event_title_snapshot }}</h3>
      <p>
        {{ ticket.order_item?.tier_name_snapshot }} ·
        {{ dateTime(ticket.order_item?.session_starts_at_snapshot) }}
      </p>
      <small>票号 {{ ticket.ticket_no }}</small>
      <small v-if="ticket.place_label" class="ticket-place">位置 {{ ticket.place_label }}</small>
      <small v-if="attendee" class="ticket-attendee">
        观演人 {{ attendee.name }} · {{ attendee.id_number_masked }}
      </small>
    </div>
    <div class="ticket-code">
      <QrcodeVue
        v-if="ticket.status === 'valid'"
        :value="ticket.credential"
        :size="132"
        level="M"
        render-as="svg"
      />
      <div v-else class="ticket-code-state">
        <strong>{{ ticketStatus[ticket.status]?.label }}</strong>
        <small v-if="ticket.used_at">{{ dateTime(ticket.used_at) }}</small>
        <small v-else-if="ticket.revoked_at">{{ dateTime(ticket.revoked_at) }}</small>
      </div>
      <b>{{ ticketStatus[ticket.status]?.label }}</b>
    </div>
  </article>
</template>

<style scoped>
.electronic-ticket {
  min-height: 194px;
  padding: 24px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-md);
  display: grid;
  grid-template-columns: 1fr 150px;
  gap: 20px;
  background: rgba(255,255,255,.24);
  position: relative;
}
.electronic-ticket::before {
  content: '';
  position: absolute;
  top: 0;
  bottom: 0;
  right: 173px;
  border-left: 1px dashed var(--line-strong);
}
.ticket-copy > span { color: var(--red); font-size: 11px; font-weight: 700; letter-spacing: .08em; }
.ticket-copy h3 { margin: 8px 0; font: 700 18px var(--font-display); }
.ticket-copy p, .ticket-copy small { color: var(--muted); }
.ticket-copy .ticket-attendee, .ticket-copy .ticket-place { margin-top: 8px; display: block; color: var(--ink); }
.ticket-code { display: grid; place-items: center; gap: 8px; }
.ticket-code b { font-size: 12px; }
.ticket-code-state { min-height: 132px; display: grid; place-content: center; text-align: center; color: var(--muted); }
.electronic-ticket.valid { border-color: rgba(181,52,41,.45); }
.electronic-ticket.used, .electronic-ticket.revoked { opacity: .72; }
@media (max-width: 780px) {
  .electronic-ticket { grid-template-columns: 1fr; }
  .electronic-ticket::before { display: none; }
}
</style>
