// Thin client for the Go API. Paths are relative (/api/...) and proxied in dev / by nginx in Docker.
const API_KEY = import.meta.env.VITE_API_KEY || 'apitest'

async function request(path, options = {}) {
  const res = await fetch(`/api${path}`, options)
  const text = await res.text()
  let body = null
  try { body = text ? JSON.parse(text) : null } catch { body = { message: text } }
  if (!res.ok) {
    const err = new Error(body?.message || `Request failed (${res.status})`)
    err.status = res.status
    throw err
  }
  return body
}

export const listProducts = () => request('/product')
export const getProduct = (id) => request(`/product/${encodeURIComponent(id)}`)

export const placeOrder = ({ items, couponCode }) =>
  request('/order', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', api_key: API_KEY },
    body: JSON.stringify({ items, ...(couponCode ? { couponCode } : {}) }),
  })
