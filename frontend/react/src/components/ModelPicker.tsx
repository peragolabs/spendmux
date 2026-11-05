import { useEffect, useState } from 'react'

const OTHER = '__other__'

/**
 * Model chooser: a real <select> of the provider's live models (every entry
 * visible on click) plus "Other…" which reveals a free-text id field.
 * A <datalist> was tried first but browsers filter it by the current value,
 * so a pre-filled box looked like it had one model.
 */
export function ModelPicker({ models, value, onChange, status, className, allowOther = false }: {
  models: string[]
  value: string
  onChange: (v: string) => void
  status?: string
  className?: string
  /** Show "Other — type a model id…". Only needed when the list is not live from the provider. */
  allowOther?: boolean
}) {
  const inList = models.includes(value)
  const [other, setOther] = useState(!inList && value !== '')
  useEffect(() => { if (inList) setOther(false) }, [inList])

  const selectCls = 'h-10 w-full rounded-lg border border-input bg-background px-3 text-sm'
  return (
    <div className={className}>
      {models.length > 0 ? (
        <select
          value={other ? OTHER : (inList ? value : '')}
          onChange={e => {
            if (e.target.value === OTHER) { setOther(true); onChange('') }
            else { setOther(false); onChange(e.target.value) }
          }}
          className={selectCls}
        >
          {!inList && !other && <option value="">Pick a model…</option>}
          {models.map(m => <option key={m} value={m}>{m}</option>)}
          {allowOther && <option value={OTHER}>Other — type a model id…</option>}
        </select>
      ) : null}
      {((allowOther && other) || models.length === 0) && (
        <input
          value={value}
          onChange={e => onChange(e.target.value)}
          placeholder="model id as the provider names it"
          className={selectCls + (models.length ? ' mt-2' : '')}
          autoComplete="off"
        />
      )}
      {status && <p className="mt-1 text-xs text-muted-foreground">{status}</p>}
    </div>
  )
}
