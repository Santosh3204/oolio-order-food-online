import { useEffect, useMemo, useState } from 'react'
import { listProducts, placeOrder } from './api.js'

const money = (n) => `$${n.toFixed(2)}`
const COUPON_RE = /^[A-Za-z0-9]{8,10}$/

export default function App() {
  const [products, setProducts] = useState([])
  const [loadError, setLoadError] = useState('')
  const [cart, setCart] = useState({}) // productId -> quantity
  const [couponInput, setCouponInput] = useState('')
  const [coupon, setCoupon] = useState('') // applied (pending server validation)
  const [couponError, setCouponError] = useState('')
  const [placing, setPlacing] = useState(false)
  // result of the last "Place order": { ok: true, order, coupon, total } | { ok: false, status, title, message }
  const [result, setResult] = useState(null)

  useEffect(() => {
    listProducts().then(setProducts).catch((e) => setLoadError(e.message))
  }, [])

  const lines = useMemo(
    () => products.filter((p) => cart[p.id] > 0).map((p) => ({ ...p, quantity: cart[p.id] })),
    [products, cart],
  )
  const total = lines.reduce((sum, l) => sum + l.price * l.quantity, 0)

  const setQty = (id, qty) =>
    setCart((c) => {
      const next = { ...c }
      if (qty <= 0) delete next[id]
      else next[id] = qty
      return next
    })

  const applyCoupon = (e) => {
    e.preventDefault()
    const code = couponInput.trim()
    if (!COUPON_RE.test(code)) {
      setCouponError('Coupon must be 8 to 10 letters/digits')
      return
    }
    setCouponError('')
    setCoupon(code)
  }
  const removeCoupon = () => {
    setCoupon('')
    setCouponInput('')
    setCouponError('')
  }

  const failureTitle = (status) =>
    ({
      400: 'Invalid order',
      401: 'Not authorized',
      403: 'Access denied',
      422: 'Order rejected',
      503: 'Service unavailable',
    })[status] || 'Order failed'

  const submit = async () => {
    setPlacing(true)
    setResult(null)
    const snapshot = { lines, total, coupon }
    try {
      const res = await placeOrder({
        items: lines.map((l) => ({ productId: l.id, quantity: l.quantity })),
        couponCode: coupon,
      })
      setResult({ ok: true, order: res, ...snapshot })
      setCart({})
    } catch (err) {
      if (coupon && err.status === 422 && /coupon/i.test(err.message)) {
        setCouponError(err.message) // also flag the coupon field
      }
      const network = err.status === undefined
      setResult({
        ok: false,
        status: err.status,
        title: network ? 'Cannot reach the server' : failureTitle(err.status),
        message: network
          ? 'Check that the API is running and try again.'
          : err.status === 503
            ? 'The coupon service is starting up. Please retry in a moment.'
            : err.message,
      })
    } finally {
      setPlacing(false)
    }
  }

  return (
    <div className="app">
      <header>
        <h1>🍽️ Order Food Online</h1>
      </header>

      <main>
        <section className="menu">
          <h2>Menu</h2>
          {loadError && <p className="error">Could not load products: {loadError}</p>}
          {!loadError && products.length === 0 && <p className="muted">Loading…</p>}
          <ul className="products">
            {products.map((p) => (
              <li key={p.id} className="product">
                <div>
                  <strong>{p.name}</strong>
                  <span className="tag">{p.category}</span>
                  <div className="price">{money(p.price)}</div>
                </div>
                <div className="qty">
                  <button onClick={() => setQty(p.id, (cart[p.id] || 0) - 1)} disabled={!cart[p.id]} aria-label={`remove one ${p.name}`}>−</button>
                  <span>{cart[p.id] || 0}</span>
                  <button onClick={() => setQty(p.id, (cart[p.id] || 0) + 1)} aria-label={`add one ${p.name}`}>+</button>
                </div>
              </li>
            ))}
          </ul>
        </section>

        <aside className="cart">
          <h2>Your order</h2>
          {lines.length === 0 ? (
            <p className="muted">Cart is empty. Add something from the menu.</p>
          ) : (
            <ul className="lines">
              {lines.map((l) => (
                <li key={l.id}>
                  <span>{l.quantity} × {l.name}</span>
                  <span>{money(l.price * l.quantity)}</span>
                </li>
              ))}
            </ul>
          )}

          <form className="coupon" onSubmit={applyCoupon}>
            <label htmlFor="coupon">Promo code</label>
            <div className="row">
              <input
                id="coupon"
                value={couponInput}
                onChange={(e) => setCouponInput(e.target.value.toUpperCase())}
                placeholder="e.g. HAPPYHRS"
                maxLength={10}
                disabled={!!coupon}
              />
              {coupon ? (
                <button type="button" className="secondary" onClick={removeCoupon}>Remove</button>
              ) : (
                <button type="submit" className="secondary">Apply</button>
              )}
            </div>
            {coupon && !couponError && <p className="ok">Code <b>{coupon}</b> added — verified when you place the order.</p>}
            {couponError && <p className="error">{couponError}</p>}
          </form>

          <div className="total"><span>Total</span><strong>{money(total)}</strong></div>
          <button className="primary" onClick={submit} disabled={lines.length === 0 || placing}>
            {placing ? 'Placing…' : 'Place order'}
          </button>
        </aside>
      </main>

      {result && (
        <section className={`result ${result.ok ? 'success' : 'failure'}`} role="status" aria-live="polite">
          <button className="close" onClick={() => setResult(null)} aria-label="Dismiss">×</button>
          {result.ok ? (
            <>
              <h2>✅ Order placed successfully</h2>
              <p>Order ID: <code>{result.order.id}</code></p>
              <ul>
                {result.order.items.map((it, i) => {
                  const p = result.order.products[i]
                  return (
                    <li key={`${it.productId}-${i}`}>
                      <span>{it.quantity} × {p?.name ?? `product ${it.productId}`}</span>
                      <span>{p ? money(p.price * it.quantity) : ''}</span>
                    </li>
                  )
                })}
              </ul>
              <p className="sum"><span>Total</span><strong>{money(result.total)}</strong></p>
              {result.coupon && <p>Coupon <b>{result.coupon}</b> was accepted.</p>}
            </>
          ) : (
            <>
              <h2>❌ {result.title}</h2>
              <p>{result.message}</p>
              {result.status && <p className="muted">HTTP status {result.status}</p>}
              <p className="muted">Your cart has not been changed — fix the problem and place the order again.</p>
            </>
          )}
        </section>
      )}
    </div>
  )
}
