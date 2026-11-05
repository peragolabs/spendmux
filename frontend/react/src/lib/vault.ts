/**
 * Browser-side sealing of provider keys.
 *
 * The plaintext never leaves this tab: a random AES-256-GCM data key encrypts
 * the credential, the vault's RSA-OAEP(SHA-256) public key wraps the data key,
 * and only the envelope is sent. The server adds its own encryption layer at
 * rest and unwraps in memory per request. If WebCrypto is unavailable we
 * refuse rather than fall back to sending plaintext.
 */

export type VaultPublicKey = { alg: string; kid: string; spki_pem: string }
export type SealedKey = { envelope: string; hint: string }

let cached: Promise<{ key: CryptoKey; kid: string }> | null = null

function pemToDer(pem: string): ArrayBuffer {
  const b64 = pem.replace(/-----[^-]+-----/g, '').replace(/\s+/g, '')
  const bin = atob(b64)
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out.buffer
}

function b64(buf: ArrayBuffer | Uint8Array): string {
  const bytes = buf instanceof Uint8Array ? buf : new Uint8Array(buf)
  let s = ''
  for (const b of bytes) s += String.fromCharCode(b)
  return btoa(s)
}

async function vaultKey(): Promise<{ key: CryptoKey; kid: string }> {
  if (!cached) {
    cached = (async () => {
      const r = await fetch('/api/vault/public-key', { credentials: 'include' })
      if (!r.ok) throw new Error('Vault public key unavailable')
      const j = (await r.json()) as VaultPublicKey
      if (j.alg !== 'RSA-OAEP-256+A256GCM') throw new Error('Unsupported vault algorithm')
      const key = await crypto.subtle.importKey('spki', pemToDer(j.spki_pem), { name: 'RSA-OAEP', hash: 'SHA-256' }, false, ['wrapKey', 'encrypt'])
      return { key, kid: j.kid }
    })().catch(e => { cached = null; throw e })
  }
  return cached
}

export function canSeal(): boolean {
  return typeof crypto !== 'undefined' && !!crypto.subtle && typeof TextEncoder !== 'undefined'
}

/** Seal a credential string. `hint` is the last 4 characters for display only. */
export async function sealProviderKey(plaintext: string): Promise<SealedKey> {
  if (!canSeal()) throw new Error('This browser cannot encrypt in-page (WebCrypto unavailable); the key was not sent.')
  const { key: pub, kid } = await vaultKey()
  const dataKey = await crypto.subtle.generateKey({ name: 'AES-GCM', length: 256 }, true, ['encrypt'])
  const iv = crypto.getRandomValues(new Uint8Array(12))
  const ct = await crypto.subtle.encrypt({ name: 'AES-GCM', iv }, dataKey, new TextEncoder().encode(plaintext))
  const wk = await crypto.subtle.wrapKey('raw', dataKey, pub, { name: 'RSA-OAEP' })
  const envelope = JSON.stringify({ alg: 'RSA-OAEP-256+A256GCM', kid, wk: b64(wk), iv: b64(iv), ct: b64(ct) })
  const trimmed = plaintext.trim()
  return { envelope, hint: trimmed.slice(-4) }
}
