import { useEffect, useState } from 'react'

import { Card } from '@/components/ui/card'

type Site = { operator: string; support_email: string; legal_updated: string }

function useSite(): Site {
  const [site, setSite] = useState<Site>({ operator: 'Spendmux', support_email: '', legal_updated: '' })
  useEffect(() => {
    fetch('/api/site').then(r => (r.ok ? r.json() : null)).then(d => { if (d) setSite(d) }).catch(() => {})
  }, [])
  return site
}

function Legal({ eyebrow, title, intro, children }: { eyebrow: string; title: string; intro: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="mx-auto max-w-3xl px-5 pb-24 pt-14 sm:px-8">
      <p className="smx-rise text-xs font-semibold uppercase tracking-[0.2em] text-primary">{eyebrow}</p>
      <h1 className="smx-rise mt-2 font-display text-4xl [animation-delay:90ms]">{title}</h1>
      <p className="smx-rise mt-4 text-muted-foreground [animation-delay:180ms]">{intro}</p>
      <div className="mt-8 grid gap-4 [&_code]:font-mono [&_code]:text-primary [&_a]:text-primary [&_a]:underline">{children}</div>
    </div>
  )
}

function Section({ id, title, children }: { id: string; title: string; children: React.ReactNode }) {
  return (
    <Card id={id} data-reveal className="p-6">
      <h2 className="font-display text-xl">{title}</h2>
      <div className="mt-2 grid gap-2 text-sm leading-relaxed text-muted-foreground [&_strong]:text-foreground [&_ul]:grid [&_ul]:gap-1.5 [&_ul]:pl-5 [&_li]:list-disc">{children}</div>
    </Card>
  )
}

function Contact({ site }: { site: Site }) {
  return site.support_email ? <> Questions or requests: <a href={`mailto:${site.support_email}`}>{site.support_email}</a>.</> : null
}

/* ── Privacy ─────────────────────────────────────────────────────────── */

export function PrivacyPage() {
  const site = useSite()
  return (
    <Legal eyebrow={`Legal · updated ${site.legal_updated}`} title="Privacy Policy"
      intro={<>Spendmux is a proxy: your applications send requests to us, we forward them to the AI provider using keys you supplied, and we return the provider's response. This policy explains exactly what {site.operator} ("we") keeps along the way, and what we do not.</>}>
      <Section id="never" title="Request content and secrets">
        <ul>
          <li><strong>Prompts and completions.</strong> Requests are forwarded to your selected provider. Response caching can persist encrypted responses, and embedding caching is enabled by default. Memory features can retain conversation content, and Playground chats are saved when the Save toggle is on. These features mean some request or response content can be stored and included in backups.</li>
          <li><strong>Provider keys in the clear.</strong> Your browser encrypts each key with the vault's public key (RSA-OAEP-wrapped AES-256-GCM) before sending it, so the plaintext never crosses the network or reaches storage. Because the service must sign requests to your provider on your behalf, the server can unwrap the key in memory for that purpose only. At rest the sealed envelope is encrypted again with a server-side secret. It is unwrapped in memory only to sign the outbound request to the provider, then discarded. Keys are never logged or displayed after you save them; the UI shows the last four characters only.</li>
          <li><strong>Bearer tokens in the clear.</strong> Your Spendmux tokens are stored as a SHA-256 hash. The plaintext is shown once, when minted.</li>
        </ul>
      </Section>
      <Section id="stored" title="What we store">
        <ul>
          <li><strong>Account:</strong> the name, email address, avatar URL, and user id returned by GitHub, Google, or Apple when you sign in. We do not receive or keep your password.</li>
          <li><strong>Per request (usage events):</strong> provider, model, endpoint, prompt / completion / cached / reasoning token counts, estimated list-price cost, latency, time-to-first-token, HTTP status, whether the response streamed, any <code>X-Spendmux-Session</code> or <code>X-Spendmux-Tenant</code> label you attached, and — when request history is on — the provider's error text for failed calls, truncated to 500 characters.</li>
          <li><strong>Loop detection:</strong> a SHA-256 fingerprint of the request body, kept only for the detection window (minutes). The body itself is not kept.</li>
          <li><strong>Vault and configuration:</strong> encrypted provider keys, base-URL overrides, bearer-token settings (scopes, caps, rate limits), fallback ladders, and tenant caps.</li>
          <li><strong>Cache and memory:</strong> encrypted cached responses and embeddings, plus conversation content retained by memory features. Provider error text recorded in request history may also contain parts of a request.</li>
          <li><strong>Playground chats:</strong> stored only while the "Save" toggle is on. You can delete them at any time.</li>
          <li><strong>Sessions and operational logs:</strong> a sign-in session record, and request path, status, duration, and a random request id — not request bodies — retained for a limited period for reliability and abuse prevention.</li>
        </ul>
        <p>You can reduce what is kept: on <a href="/account#privacy">Account → Privacy &amp; data</a>, turn off per-request history. Totals and per-model spend are still recorded so your caps keep working; the per-request table and error text are not.</p>
      </Section>
      <Section id="use" title="How we use it">
        <ul>
          <li>To authenticate you, authorize requests with your bearer token, and route each call to the provider with the key you saved.</li>
          <li>To enforce the caps, rate limits, session kills, tenant caps, and loop detection you configure.</li>
          <li>To show you your own usage and cost on the Usage page.</li>
          <li>To keep the service secure and reliable.</li>
        </ul>
        <p><strong>We do not sell or rent your data, do not use your traffic to train models, and do not show advertising.</strong></p>
      </Section>
      <Section id="providers" title="AI providers">
        <p>When you use Spendmux, your request content is sent to the provider you chose (for example Groq, Cerebras, Novita, OpenAI, or Anthropic) under your own account with that provider. Their handling of that content is governed by their terms and privacy policies, not ours. Spendmux does not resell provider access and is not a party to your agreement with them.</p>
      </Section>
      <Section id="cookies" title="Cookies">
        <p>We set only cookies that are necessary for the service to work:</p>
        <ul>
          <li><code>spendmux_session</code> — your sign-in session.</li>
          <li>A short-lived state cookie used only during the OAuth sign-in handshake.</li>
        </ul>
        <p>We use no analytics, advertising, or cross-site tracking cookies. Your browser's local storage remembers UI preferences such as the theme and playground toggles.</p>
      </Section>
      <Section id="processors" title="Who else touches your data">
        <ul>
          <li><strong>Hosting and database</strong> — the infrastructure this service runs on. Data is encrypted in transit (TLS) and provider keys are encrypted at rest.</li>
          <li><strong>GitHub, Google, or Apple</strong> — sign-in only; we request the minimum profile scope.</li>
          <li><strong>Your chosen AI providers</strong> — receive your request content as described above.</li>
        </ul>
      </Section>
      <Section id="retention" title="Retention and deletion">
        <ul>
          <li>Usage events are kept while your account is active so caps and history work. Loop fingerprints and token reservations expire automatically within minutes; sign-in sessions expire and are purged.</li>
          <li>Deleting your account on <a href="/account">/account</a> permanently removes your profile, sign-in identities, encrypted provider keys, bearer tokens, usage events, tenant caps, fallback ladders, saved playground chats, sessions, and organisations you created.</li>
          <li>Backups are encrypted and rotate out within 30 days of a deletion.</li>
        </ul>
      </Section>
      <Section id="rights" title="Your rights">
        <p>Wherever you are, you can see what we hold (it is all visible in the app), correct it, export it, or delete it. If you are in the EU/EEA, UK, or a jurisdiction with similar law, you also have the rights of access, rectification, erasure, restriction, portability, and objection, and the right to complain to your supervisory authority. Our legal basis for processing is performance of the contract you accept when you create an account, and our legitimate interest in keeping the service secure.</p>
      </Section>
      <Section id="security" title="Security">
        <p>Provider keys: sealed in the browser with an RSA-4096 vault key and AES-256-GCM, then encrypted again at rest with a separate server secret; unwrapped in memory per request. Tokens: SHA-256 hashed. Transport: HTTPS with HSTS. Browser protections: strict Content-Security-Policy, same-origin checks on all state-changing requests, and cookies that are HTTP-only, Secure, and SameSite. If you believe you have found a vulnerability, please contact us before disclosing it publicly.</p>
      </Section>
      <Section id="changes" title="Changes and contact">
        <p>We will post changes to this policy here and update the date at the top. Material changes will also be announced in the app.<Contact site={site} /></p>
      </Section>
    </Legal>
  )
}

/* ── Terms ───────────────────────────────────────────────────────────── */

export function TermsPage() {
  const site = useSite()
  const op = site.operator
  return (
    <Legal eyebrow={`Legal · updated ${site.legal_updated}`} title="Terms of Service"
      intro={<>These terms govern your use of this Spendmux instance, operated by {op} ("we", "us"). By creating an account or sending a request through the service you agree to them. The Spendmux source code is licensed separately under the MIT License and is not covered here.</>}>
      <Section id="service" title="1. What the service is">
        <p>Spendmux is a bring-your-own-key proxy. You store your own AI provider API keys with us, encrypted; we mint bearer tokens for your applications; we forward each request to the provider using your key, enforce the spending limits you configure, and report usage back to you. We do not sell model access, we do not resell tokens, and we are not a party to your agreement with any provider.</p>
      </Section>
      <Section id="account" title="2. Your account">
        <ul>
          <li>You must be at least 18 and able to enter a binding contract. Sign-in is through GitHub, Google, or Apple; you are responsible for the security of those accounts.</li>
          <li>You are responsible for everything done with your bearer tokens. Treat them as secrets, scope and cap them, and rotate or delete them on <a href="/keys">/keys</a> if they leak.</li>
        </ul>
      </Section>
      <Section id="keys" title="3. Your provider keys and your provider bill">
        <ul>
          <li>You warrant that you are authorized to use each provider key you store and that your use complies with that provider's terms.</li>
          <li><strong>All provider charges are yours.</strong> Requests are made under your provider account. Spendmux's spending caps are estimates at list price and are enforced before we forward a request; they are a safeguard, not a guarantee. Provider pricing, rounding, streaming responses, retries you initiate, and calls that bypass Spendmux can all cause your provider invoice to differ from what the Usage page shows. We are not liable for provider charges.</li>
          <li>We may block or rate-limit traffic that we reasonably believe is abusive, illegal, or puts the service or your keys at risk.</li>
        </ul>
      </Section>
      <Section id="aup" title="4. Acceptable use">
        <p>You will not use the service to:</p>
        <ul>
          <li>violate any law, any provider's usage policy, or anyone's rights;</li>
          <li>store keys or route traffic for accounts you do not control;</li>
          <li>probe, scan, or attack the service or other users' data;</li>
          <li>resell access to the service, or use it to operate a public model-reselling service.</li>
        </ul>
        <p>You are solely responsible for the content you send through the service and for what your applications do with the responses.</p>
      </Section>
      <Section id="data" title="5. Your data">
        <p>What we store and how to manage it are described in the <a href="/privacy">Privacy Policy</a>, which is part of these terms. We store request metadata to enforce caps and show usage. Cache, memory, and saved Playground features can retain content; provider keys are encrypted. Account deletion is available from <a href="/account">/account</a>.</p>
      </Section>
      <Section id="availability" title="6. Availability and support">
        <p>We aim for high availability but do not promise uninterrupted service. Providers, networks, and our own infrastructure can fail. Configure fallback ladders and keep your provider keys so you can call providers directly if Spendmux is unavailable. Support is by email on a reasonable-efforts basis.</p>
      </Section>
      <Section id="ip" title="7. Intellectual property">
        <p>The Spendmux source code is available under the MIT License. Your keys, your prompts, and your applications remain yours; you grant us only the limited right to process them as needed to run the service for you.</p>
      </Section>
      <Section id="liability" title="8. Disclaimer and limitation of liability">
        <p>The service is provided "as is" and "as available", without warranties of any kind, express or implied, including fitness for a particular purpose and non-infringement. To the fullest extent permitted by law, {op} is not liable for indirect, incidental, special, consequential, or punitive damages, or for loss of profits, data, or provider charges, arising from the service. Our total liability for any claim is limited to the amount you paid us, if anything, in the three months before the event giving rise to the claim. Some jurisdictions do not allow these limitations; in that case they apply to the extent permitted.</p>
      </Section>
      <Section id="termination" title="9. Termination">
        <p>You can delete your account at any time from <a href="/account">/account</a>. We may suspend or terminate accounts that breach these terms, with notice where practical. On termination we delete your data as described in the Privacy Policy. Sections 3, 4, 7, 8, and 10 survive termination.</p>
      </Section>
      <Section id="changes" title="10. Changes, governing law, contact">
        <p>We may update these terms; we will post the new version here with a new date and, for material changes, notify you in the app or by email at least 14 days before they take effect. Continued use after that date is acceptance. These terms are governed by the laws of the jurisdiction in which {op} is established, without regard to conflict-of-law rules.<Contact site={site} /></p>
      </Section>
    </Legal>
  )
}
