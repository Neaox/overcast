/**
 * CreatePoolDialog — multi-section dialog for creating a Cognito User Pool.
 *
 * Surfaces the most commonly needed settings up-front:
 *  - Pool name
 *  - Sign-in identifier (username / email / phone / email-or-phone)
 *  - Password policy (min length + complexity)
 *  - Admin-only creation (disable self-service sign-up)
 */
import { useState, useCallback } from "react"
import { AtSign, Phone, User, UserCircle2 } from "lucide-react"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { createPoolMutationOptions, cognitoKeys } from "@/features/cognito/data"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { FormField } from "@/components/ui/form"
import {
  Dialog,
  DialogContent,
  DialogBody,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Spinner } from "@/components/ui/primitives"
import { cn } from "@/lib/utils"

// ─── Types ────────────────────────────────────────────────────────────────────

type SignInMethod = "username" | "email" | "phone" | "email_or_phone"

interface SignInOption {
  value: SignInMethod
  icon: React.ReactNode
  label: string
  description: string
}

// ─── Constants ────────────────────────────────────────────────────────────────

const SIGN_IN_OPTIONS: SignInOption[] = [
  {
    value: "username",
    icon: <User className="h-4 w-4" />,
    label: "Username",
    description: "Users choose a unique username",
  },
  {
    value: "email",
    icon: <AtSign className="h-4 w-4" />,
    label: "Email address",
    description: "Sign in with email address",
  },
  {
    value: "phone",
    icon: <Phone className="h-4 w-4" />,
    label: "Phone number",
    description: "Sign in with phone number",
  },
  {
    value: "email_or_phone",
    icon: <UserCircle2 className="h-4 w-4" />,
    label: "Email or phone",
    description: "Either email or phone number",
  },
]

function signInMethodToAttributes(method: SignInMethod): string[] {
  switch (method) {
    case "email":
      return ["email"]
    case "phone":
      return ["phone_number"]
    case "email_or_phone":
      return ["email", "phone_number"]
    default:
      return []
  }
}

// ─── CreatePoolDialog ─────────────────────────────────────────────────────────

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function CreatePoolDialog({ open, onOpenChange }: Props) {
  const [name, setName] = useState("")
  const [signIn, setSignIn] = useState<SignInMethod>("username")
  const [adminOnly, setAdminOnly] = useState(false)
  const [minLength, setMinLength] = useState(8)
  const [requireUpper, setRequireUpper] = useState(true)
  const [requireLower, setRequireLower] = useState(true)
  const [requireNumbers, setRequireNumbers] = useState(true)
  const [requireSymbols, setRequireSymbols] = useState(true)

  const reset = useCallback(() => {
    setName("")
    setSignIn("username")
    setAdminOnly(false)
    setMinLength(8)
    setRequireUpper(true)
    setRequireLower(true)
    setRequireNumbers(true)
    setRequireSymbols(true)
  }, [])

  const createMut = useResourceMutation({
    options: createPoolMutationOptions(),
    invalidateKeys: [cognitoKeys.pools()],
    successTitle: "User pool created",
    successDescription: (opts: { name: string }) => opts.name,
    onSuccess: () => {
      onOpenChange(false)
      reset()
    },
  })

  function handleCreate() {
    if (!name.trim()) return
    createMut.mutate({
      name: name.trim(),
      usernameAttributes: signInMethodToAttributes(signIn),
      adminOnly,
      passwordPolicy: {
        minimumLength: minLength,
        requireUppercase: requireUpper,
        requireLowercase: requireLower,
        requireNumbers,
        requireSymbols,
        temporaryPasswordValidityDays: 7,
      },
    })
  }

  function handleClose() {
    onOpenChange(false)
    setTimeout(reset, 150)
  }

  const complexityOptions = [
    { label: "Uppercase (A–Z)", checked: requireUpper, set: setRequireUpper },
    { label: "Lowercase (a–z)", checked: requireLower, set: setRequireLower },
    { label: "Numbers (0–9)", checked: requireNumbers, set: setRequireNumbers },
    { label: "Symbols (!@#…)", checked: requireSymbols, set: setRequireSymbols },
  ]

  return (
    <Dialog open={open} onOpenChange={(v) => !v && handleClose()}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>Create User Pool</DialogTitle>
        </DialogHeader>

        <DialogBody className="flex flex-col gap-5 py-1">
          {/* Pool name */}
          <FormField label="Pool name" required>
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="my-user-pool"
              autoFocus
            />
          </FormField>

          {/* Sign-in identifier */}
          <div className="flex flex-col gap-2">
            <p className="text-sm font-medium text-fg-muted">Sign-in identifier</p>
            <div className="grid grid-cols-2 gap-2">
              {SIGN_IN_OPTIONS.map((opt) => (
                <SignInCard
                  key={opt.value}
                  active={signIn === opt.value}
                  icon={opt.icon}
                  label={opt.label}
                  description={opt.description}
                  onClick={() => setSignIn(opt.value)}
                />
              ))}
            </div>
          </div>

          {/* Password policy */}
          <div className="flex flex-col gap-3 rounded-lg border border-border p-3">
            <p className="text-sm font-medium text-fg">Password policy</p>
            <div className="flex items-center gap-3">
              <label htmlFor="pool-min-length" className="shrink-0 text-sm text-fg-muted">
                Minimum length
              </label>
              <Input
                id="pool-min-length"
                type="number"
                className="w-20"
                min={6}
                max={99}
                value={minLength}
                onChange={(e) =>
                  setMinLength(Math.max(6, Math.min(99, parseInt(e.target.value) || 8)))
                }
              />
            </div>
            <div className="grid grid-cols-2 gap-x-4 gap-y-2">
              {complexityOptions.map(({ label, checked, set }) => (
                <label
                  key={label}
                  className="flex cursor-pointer items-center gap-2 text-sm text-fg-muted"
                >
                  <Switch checked={checked} onCheckedChange={set} />
                  {label}
                </label>
              ))}
            </div>
          </div>

          {/* Admin-only creation */}
          <label className="flex cursor-pointer items-center justify-between gap-3 rounded-lg border border-border p-3">
            <div>
              <p className="text-sm font-medium text-fg">Admin-only creation</p>
              <p className="text-xs text-fg-muted">
                Disable self-service sign-up — only admins can create users
              </p>
            </div>
            <Switch checked={adminOnly} onCheckedChange={setAdminOnly} />
          </label>
        </DialogBody>

        <DialogFooter>
          <Button variant="ghost" onClick={handleClose} disabled={createMut.isPending}>
            Cancel
          </Button>
          <Button onClick={handleCreate} disabled={!name.trim() || createMut.isPending}>
            {createMut.isPending && <Spinner className="mr-1.5 h-3.5 w-3.5" />}
            Create pool
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── SignInCard ───────────────────────────────────────────────────────────────

function SignInCard({
  active,
  icon,
  label,
  description,
  onClick,
}: {
  active: boolean
  icon: React.ReactNode
  label: string
  description: string
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "flex flex-col gap-1 rounded-md border px-3 py-2.5 text-left text-sm transition-colors",
        active
          ? "border-accent bg-accent/10 text-accent"
          : "border-border text-fg-muted hover:border-fg-subtle",
      )}
    >
      <span className="flex items-center gap-2 font-medium">
        {icon}
        {label}
      </span>
      <span className="text-xs opacity-70">{description}</span>
    </button>
  )
}
