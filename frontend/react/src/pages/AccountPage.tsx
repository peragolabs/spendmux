import { useCallback, useEffect, useRef, useState } from 'react'
import { Camera, Download, Github, Link2, Loader2, LogOut, Trash2, Unlink } from 'lucide-react'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Avatar, AvatarFallback, AvatarImage } from '@/components/ui/avatar'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Separator } from '@/components/ui/separator'
import { AppleIcon, GoogleIcon } from '@/components/ProviderIcons'
import { PrivacyCard } from '@/components/PrivacyCard'

type UserProfile = {
  email?: string
  provider?: string
  created_at?: number
  avatar_display_url?: string
  keep_request_history?: boolean
  role?: string
  email_masked?: string
  name?: string
}

type Identity = { provider: string; connected_at: number }

const KNOWN_PROVIDERS: { id: string; label: string; icon: React.ReactNode }[] = [
  { id: 'google', label: 'Google', icon: <GoogleIcon /> },
  { id: 'github', label: 'GitHub', icon: <Github className="h-4 w-4" /> },
  { id: 'apple', label: 'Apple', icon: <AppleIcon /> },
]


export default function AccountPage() {
  const [profile, setProfile] = useState<UserProfile | null>(null)
  const [uploading, setUploading] = useState(false)
  const [uploadError, setUploadError] = useState('')
  const [actionLoading, setActionLoading] = useState<string | null>(null)
  const [showDeleteModal, setShowDeleteModal] = useState(false)
  const [deleteConfirm, setDeleteConfirm] = useState('')
  const [deleteError, setDeleteError] = useState('')
  const fileRef = useRef<HTMLInputElement>(null)

  const [identities, setIdentities] = useState<Identity[]>([])
  const [hasPassword, setHasPassword] = useState(true)
  const [identitiesError, setIdentitiesError] = useState('')
  const [unlinking, setUnlinking] = useState<string | null>(null)

  const loadIdentities = useCallback(() => {
    return fetch('/api/auth/identities', { credentials: 'include' })
      .then(r => (r.ok ? r.json() : null))
      .then(data => {
        if (data) {
          setIdentities(data.identities || [])
          setHasPassword(Boolean(data.has_password))
        }
      })
      .catch(() => {})
  }, [])

  async function handleDisconnect(provider: string) {
    setIdentitiesError('')
    setUnlinking(provider)
    try {
      const r = await fetch(`/api/auth/identities/${provider}`, { method: 'DELETE', credentials: 'include' })
      const data = await r.json().catch(() => ({}))
      if (!r.ok) throw new Error(data.error || 'Could not disconnect')
      await loadIdentities()
    } catch (err) {
      setIdentitiesError(err instanceof Error ? err.message : 'Could not disconnect')
    } finally {
      setUnlinking(null)
    }
  }

  const maskedEmail = profile?.email_masked || ''
  const displayName = profile?.name || maskedEmail.split('@')[0]?.replace(/•/g, '') || 'there'
  const initials = displayName.slice(0, 2).toUpperCase()
  const memberSince = profile?.created_at
    ? new Date(profile.created_at * 1000).toLocaleDateString(undefined, {
        year: 'numeric',
        month: 'long',
      })
    : 'Recently'

  function loadProfile() {
    return fetch('/api/auth/user/me', { credentials: 'include' })
      .then(r => {
        if (!r.ok) throw new Error()
        return r.json()
      })
      .then((data: UserProfile) => {
        setProfile(data)
        return data
      })
  }

  useEffect(() => {
    loadProfile()
      .then(() => loadIdentities())
      .catch(() => {
        window.location.href = '/login?next=/account'
      })
  }, [loadIdentities])

  async function handleLogout() {
    setActionLoading('logout')
    try {
      await fetch('/auth/logout', { method: 'POST', credentials: 'include' })
    } catch { /* */ }
    window.location.href = '/'
  }

  async function handleDelete() {
    setActionLoading('delete')
    setDeleteError('')
    try {
      const r = await fetch('/api/auth/account', {
        method: 'DELETE', credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ confirmation: deleteConfirm }),
      })
      const j = await r.json().catch(() => ({}))
      if (!r.ok) throw new Error(j.message || 'Could not delete the account')
      window.location.href = '/'
    } catch (e) {
      setDeleteError(e instanceof Error ? e.message : 'Could not delete the account')
      setActionLoading(null)
    }
  }

  async function handleAvatarChange(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    if (!file) return
    setUploadError('')
    setUploading(true)
    try {
      const form = new FormData()
      form.append('avatar', file)
      const r = await fetch('/api/upload/avatar', {
        method: 'POST',
        credentials: 'include',
        body: form,
      })
      if (!r.ok) {
        const data = await r.json().catch(() => ({}))
        throw new Error(data.error || 'Upload failed')
      }
      await loadProfile()
    } catch (err) {
      setUploadError(err instanceof Error ? err.message : 'Upload failed')
    } finally {
      setUploading(false)
      if (fileRef.current) fileRef.current.value = ''
    }
  }

  async function handleRemoveAvatar() {
    setUploadError('')
    setUploading(true)
    try {
      const r = await fetch('/api/upload/avatar', { method: 'DELETE', credentials: 'include' })
      if (!r.ok) throw new Error('Could not remove avatar')
      await loadProfile()
    } catch (err) {
      setUploadError(err instanceof Error ? err.message : 'Remove failed')
    } finally {
      setUploading(false)
    }
  }

  async function handleExport() {
    setActionLoading('export')
    try {
      const data = await loadProfile()
      const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = 'my-account-export.json'
      a.click()
      URL.revokeObjectURL(url)
    } finally {
      setActionLoading(null)
    }
  }

  if (!profile) {
    return (
      <div className="mx-auto max-w-6xl px-5 py-24 sm:px-8">
        <div className="flex items-center gap-3 text-muted-foreground">
          <Loader2 className="h-4 w-4 animate-spin" />
          Loading your account…
        </div>
      </div>
    )
  }

  return (
    <>
      {/* Hero */}
      <section className="relative overflow-hidden">
        <div className="hero-glow absolute inset-0 -z-10 opacity-60" aria-hidden />
        <div className="mx-auto flex max-w-6xl flex-wrap items-end justify-between gap-4 px-5 pt-16 pb-10 sm:px-8">
          <div>
            <span className="mono-eyebrow">Account</span>
            <h1 className="mt-2 font-display text-4xl md:text-5xl">Hi, {displayName}</h1>
            <p className="mt-2 text-muted-foreground">
              You&apos;re signed in via <strong className="text-foreground">{profile.provider || 'password'}</strong>.
            </p>
          </div>
          <Button
            variant="outline"
            size="sm"
            onClick={handleLogout}
            disabled={actionLoading === 'logout'}
            data-testid="account-signout-btn"
          >
            {actionLoading === 'logout' ? <Loader2 className="h-4 w-4 animate-spin" /> : <LogOut className="h-4 w-4" />}
            Sign out
          </Button>
        </div>
      </section>

      {/* Grid */}
      <section className="mx-auto max-w-6xl px-5 pb-24 sm:px-8">
        <div className="grid gap-6 lg:grid-cols-[1.1fr_1fr]">
          {/* Profile card */}
          <Card data-testid="profile-card">
            <CardHeader>
              <CardTitle>Profile</CardTitle>
              <CardDescription>Synced from your provider on each sign-in.</CardDescription>
            </CardHeader>
            <CardContent className="space-y-6">
              <div className="flex items-center gap-4">
                <button
                  type="button"
                  onClick={() => fileRef.current?.click()}
                  aria-label="Change avatar"
                  className="rounded-full ring-offset-background transition hover:ring-2 hover:ring-ring hover:ring-offset-2"
                  data-testid="avatar-upload-btn"
                >
                  <Avatar className="h-16 w-16">
                    {profile.avatar_display_url && <AvatarImage src={profile.avatar_display_url} alt="" />}
                    <AvatarFallback>{initials}</AvatarFallback>
                  </Avatar>
                </button>
                <input
                  ref={fileRef}
                  type="file"
                  accept="image/jpeg,image/png,image/webp,image/gif"
                  hidden
                  onChange={handleAvatarChange}
                  data-testid="avatar-file-input"
                />
              </div>
              {uploadError && <Alert variant="destructive"><AlertDescription>{uploadError}</AlertDescription></Alert>}
              <Separator />
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-1.5">
                  <Label>Email</Label>
                  <Input value={maskedEmail || '—'} readOnly title="Shown masked; it is encrypted at rest" />
                </div>
                <div className="space-y-1.5">
                  <Label>Provider</Label>
                  <Input value={profile.provider || '—'} readOnly />
                </div>
                <div className="space-y-1.5 sm:col-span-2">
                  <Label>Member since</Label>
                  <Input value={memberSince} readOnly />
                </div>
              </div>
            </CardContent>
          </Card>

          {/* Right column */}
          <div className="space-y-6">
            <PrivacyCard initial={profile?.keep_request_history} />

            <Card data-testid="connected-accounts-card">
              <CardHeader>
                <CardTitle>Connected accounts</CardTitle>
                <CardDescription>Sign in with more than one method — they all reach the same account.</CardDescription>
              </CardHeader>
              <CardContent className="space-y-3">
                {identitiesError && (
                  <Alert variant="destructive"><AlertDescription>{identitiesError}</AlertDescription></Alert>
                )}
                {KNOWN_PROVIDERS.map(p => {
                  const connected = identities.find(i => i.provider === p.id)
                  const onlyMethod = hasPassword === false && identities.length <= 1 && Boolean(connected)
                  return (
                    <div key={p.id} className="flex items-center justify-between gap-3 rounded-xl border border-border p-4">
                      <div className="flex items-center gap-3">
                        {p.icon}
                        <div>
                          <div className="text-sm font-medium">{p.label}</div>
                          <div className="text-xs text-muted-foreground">{connected ? 'Connected' : 'Not connected'}</div>
                        </div>
                      </div>
                      {connected ? (
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => handleDisconnect(p.id)}
                          disabled={unlinking === p.id || onlyMethod}
                          title={onlyMethod ? 'This is your only sign-in method' : undefined}
                        >
                          {unlinking === p.id ? <Loader2 className="h-4 w-4 animate-spin" /> : <Unlink className="h-4 w-4" />}
                          Disconnect
                        </Button>
                      ) : (
                        <Button asChild size="sm" variant="outline">
                          <a href={`/auth/${p.id}/link`}>
                            <Link2 className="h-4 w-4" />
                            Connect
                          </a>
                        </Button>
                      )}
                    </div>
                  )
                })}
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle>Data &amp; privacy</CardTitle>
                <CardDescription>You own your data. Export it — or delete everything.</CardDescription>
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="flex items-center justify-between gap-3 rounded-xl border border-border p-4">
                  <div>
                    <strong className="text-sm">Export data</strong>
                    <p className="text-xs text-muted-foreground">Download a JSON snapshot of your profile.</p>
                  </div>
                  <Button size="sm" variant="outline" onClick={handleExport} disabled={actionLoading === 'export'} data-testid="export-btn">
                    <Download className="h-4 w-4" />
                    {actionLoading === 'export' ? 'Preparing…' : 'Export'}
                  </Button>
                </div>
                <div className="rounded-xl border border-destructive/40 bg-destructive/5 p-4">
                  <div className="flex items-center justify-between gap-3">
                    <div>
                      <strong className="text-sm text-destructive">Delete account</strong>
                      <p className="text-xs text-muted-foreground">Removes your keys, tokens, usage, chats and organisations. Permanent.</p>
                    </div>
                    {!showDeleteModal && (
                      <Button size="sm" variant="outline" className="border-destructive/40 text-destructive hover:bg-destructive/10" onClick={() => setShowDeleteModal(true)} data-testid="delete-account-btn">
                        <Trash2 className="h-4 w-4" /> Delete…
                      </Button>
                    )}
                  </div>
                  {showDeleteModal && (
                    <form className="mt-4 grid gap-2" onSubmit={e => { e.preventDefault(); void handleDelete() }} data-testid="delete-modal">
                      <label className="text-xs text-muted-foreground" htmlFor="delete-confirm">Type your account email (<span className="font-mono">{maskedEmail}</span>) to confirm</label>
                      <Input id="delete-confirm" type="email" autoComplete="off" value={deleteConfirm} onChange={e => setDeleteConfirm(e.target.value)} placeholder="you@example.com" />
                      {deleteError && <p className="text-xs text-destructive">{deleteError}</p>}
                      <div className="flex gap-2">
                        <Button type="submit" size="sm" variant="destructive" disabled={!deleteConfirm.trim() || actionLoading === 'delete'} data-testid="confirm-delete-btn">
                          {actionLoading === 'delete' ? <Loader2 className="h-4 w-4 animate-spin" /> : <Trash2 className="h-4 w-4" />} Delete account
                        </Button>
                        <Button type="button" size="sm" variant="ghost" onClick={() => { setShowDeleteModal(false); setDeleteConfirm(''); setDeleteError('') }}>Cancel</Button>
                      </div>
                    </form>
                  )}
                </div>
              </CardContent>
            </Card>
          </div>
        </div>
      </section>

    </>
  )
}
