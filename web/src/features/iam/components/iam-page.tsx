import { useState, useMemo } from "react"
import { useQuery } from "@tanstack/react-query"
import { Users, Plus, Trash2, RefreshCw, Search } from "lucide-react"
import {
  iamUsersQueryOptions,
  iamRolesQueryOptions,
  iamPoliciesQueryOptions,
  iamGroupsQueryOptions,
  iamKeys,
  deleteUserMutationOptions,
  deleteRoleMutationOptions,
  deletePolicyMutationOptions,
  deleteGroupMutationOptions,
  createUserMutationOptions,
  createRoleMutationOptions,
  createPolicyMutationOptions,
  createGroupMutationOptions,
} from "@/features/iam/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Tabs, TabList, Tab, TabPanel } from "@/components/ui/tabs"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { InertBanner } from "@/components/inert-banner"
import { CreateResourceDialog } from "@/components/create-resource-dialog"
import { cn } from "@/lib/utils"

export function IAMPage() {
  const [tab, setTab] = useState("users")
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="IAM"
        description="Identity and Access Management"
        actions={
          <ServiceDocsButton
            service="iam"
            label="IAM"
            open={docsOpen}
            onOpen={openDocs}
            onClose={closeDocs}
          />
        }
      />
      <InertBanner serviceName="IAM" />
      <Tabs selectedKey={tab} onSelectionChange={setTab}>
        <TabList>
          <Tab id="users">Users</Tab>
          <Tab id="roles">Roles</Tab>
          <Tab id="policies">Policies</Tab>
          <Tab id="groups">Groups</Tab>
        </TabList>
        <TabPanel id="users">
          <UsersTab />
        </TabPanel>
        <TabPanel id="roles">
          <RolesTab />
        </TabPanel>
        <TabPanel id="policies">
          <PoliciesTab />
        </TabPanel>
        <TabPanel id="groups">
          <GroupsTab />
        </TabPanel>
      </Tabs>
    </div>
  )
}

// ─── Users Tab ─────────────────────────────────────────────────────────────

function UsersTab() {
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string>()
  const [filter, setFilter] = useState("")
  const { data: users = [], isLoading, isFetching, refetch } = useQuery(iamUsersQueryOptions())

  const deleteMut = useResourceMutation({
    options: deleteUserMutationOptions(),
    invalidateKeys: [iamKeys.users()],
    successTitle: "User deleted",
    onSuccess: () => setDeleteTarget(undefined),
  })

  const filtered = useMemo(
    () =>
      filter
        ? users.filter((u) => (u.UserName ?? "").toLowerCase().includes(filter.toLowerCase()))
        : users,
    [users, filter],
  )

  return (
    <div className="flex flex-col gap-3 pt-4">
      <div className="flex items-center gap-2">
        <div className="relative flex-1">
          <Search className="text-muted-foreground absolute top-1/2 left-2 h-3.5 w-3.5 -translate-y-1/2" />
          <Input
            placeholder="Filter users…"
            className="pl-8"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
        </div>
        <Button size="sm" variant="ghost" onClick={() => refetch()} disabled={isFetching}>
          <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} />
          Refresh
        </Button>
        <Button size="sm" onClick={() => setShowCreate(true)}>
          <Plus className="mr-1.5 h-3.5 w-3.5" /> Create User
        </Button>
      </div>

      {isLoading ? (
        <div className="flex justify-center py-16">
          <Spinner className="h-6 w-6" />
        </div>
      ) : filtered.length === 0 ? (
        <EmptyState
          icon={<Users className="h-6 w-6" />}
          title="No users"
          description={filter ? "No users match the filter." : "Create an IAM user to get started."}
          action={
            !filter && (
              <Button size="sm" onClick={() => setShowCreate(true)}>
                <Plus className="mr-1.5 h-3.5 w-3.5" /> Create User
              </Button>
            )
          }
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>User Name</TableHead>
              <TableHead>ARN</TableHead>
              <TableHead>Path</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {filtered.map((u) => (
              <TableRow key={u.UserName}>
                <TableCell className="font-mono text-sm">{u.UserName}</TableCell>
                <TableCell className="text-muted-foreground font-mono text-xs">{u.Arn}</TableCell>
                <TableCell>{u.Path}</TableCell>
                <TableCell className="text-right">
                  <Button
                    size="sm"
                    variant="ghost"
                    className="text-danger hover:text-danger"
                    onClick={() => setDeleteTarget(u.UserName)}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <CreateResourceDialog
        open={showCreate}
        onOpenChange={setShowCreate}
        title="Create User"
        label="User Name"
        placeholder="my-user"
        mutationOptions={createUserMutationOptions}
        invalidateKeys={[iamKeys.users()]}
        successTitle="User created"
      />
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(undefined)}
        title="Delete User"
        description={
          <>
            Delete user <span className="font-mono font-semibold">{deleteTarget}</span>? This cannot
            be undone.
          </>
        }
        confirmLabel="Delete"
        variant="danger"
        isPending={deleteMut.isPending}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget)}
      />
    </div>
  )
}

// ─── Roles Tab ─────────────────────────────────────────────────────────────

function RolesTab() {
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string>()
  const [filter, setFilter] = useState("")
  const { data: roles = [], isLoading, isFetching, refetch } = useQuery(iamRolesQueryOptions())

  const deleteMut = useResourceMutation({
    options: deleteRoleMutationOptions(),
    invalidateKeys: [iamKeys.roles()],
    successTitle: "Role deleted",
    onSuccess: () => setDeleteTarget(undefined),
  })

  const filtered = useMemo(
    () =>
      filter
        ? roles.filter((r) => (r.RoleName ?? "").toLowerCase().includes(filter.toLowerCase()))
        : roles,
    [roles, filter],
  )

  return (
    <div className="flex flex-col gap-3 pt-4">
      <div className="flex items-center gap-2">
        <div className="relative flex-1">
          <Search className="text-muted-foreground absolute top-1/2 left-2 h-3.5 w-3.5 -translate-y-1/2" />
          <Input
            placeholder="Filter roles…"
            className="pl-8"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
        </div>
        <Button size="sm" variant="ghost" onClick={() => refetch()} disabled={isFetching}>
          <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} /> Refresh
        </Button>
        <Button size="sm" onClick={() => setShowCreate(true)}>
          <Plus className="mr-1.5 h-3.5 w-3.5" /> Create Role
        </Button>
      </div>

      {isLoading ? (
        <div className="flex justify-center py-16">
          <Spinner className="h-6 w-6" />
        </div>
      ) : filtered.length === 0 ? (
        <EmptyState
          icon={<Users className="h-6 w-6" />}
          title="No roles"
          description={filter ? "No roles match the filter." : "Create an IAM role to get started."}
          action={
            !filter && (
              <Button size="sm" onClick={() => setShowCreate(true)}>
                <Plus className="mr-1.5 h-3.5 w-3.5" /> Create Role
              </Button>
            )
          }
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Role Name</TableHead>
              <TableHead>ARN</TableHead>
              <TableHead>Path</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {filtered.map((r) => (
              <TableRow key={r.RoleName}>
                <TableCell className="font-mono text-sm">{r.RoleName}</TableCell>
                <TableCell className="text-muted-foreground font-mono text-xs">{r.Arn}</TableCell>
                <TableCell>{r.Path}</TableCell>
                <TableCell className="text-right">
                  <Button
                    size="sm"
                    variant="ghost"
                    className="text-danger hover:text-danger"
                    onClick={() => setDeleteTarget(r.RoleName)}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <CreateResourceDialog
        open={showCreate}
        onOpenChange={setShowCreate}
        title="Create Role"
        label="Role Name"
        placeholder="my-role"
        mutationOptions={createRoleMutationOptions}
        invalidateKeys={[iamKeys.roles()]}
        successTitle="Role created"
      />
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(undefined)}
        title="Delete Role"
        description={
          <>
            Delete role <span className="font-mono font-semibold">{deleteTarget}</span>? This cannot
            be undone.
          </>
        }
        confirmLabel="Delete"
        variant="danger"
        isPending={deleteMut.isPending}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget)}
      />
    </div>
  )
}

// ─── Policies Tab ──────────────────────────────────────────────────────────

function PoliciesTab() {
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<{ name: string; arn: string }>()
  const [filter, setFilter] = useState("")
  const {
    data: policies = [],
    isLoading,
    isFetching,
    refetch,
  } = useQuery(iamPoliciesQueryOptions())

  const deleteMut = useResourceMutation({
    options: deletePolicyMutationOptions(),
    invalidateKeys: [iamKeys.policies()],
    successTitle: "Policy deleted",
    onSuccess: () => setDeleteTarget(undefined),
  })

  const filtered = useMemo(
    () =>
      filter
        ? policies.filter((p) => (p.PolicyName ?? "").toLowerCase().includes(filter.toLowerCase()))
        : policies,
    [policies, filter],
  )

  return (
    <div className="flex flex-col gap-3 pt-4">
      <div className="flex items-center gap-2">
        <div className="relative flex-1">
          <Search className="text-muted-foreground absolute top-1/2 left-2 h-3.5 w-3.5 -translate-y-1/2" />
          <Input
            placeholder="Filter policies…"
            className="pl-8"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
        </div>
        <Button size="sm" variant="ghost" onClick={() => refetch()} disabled={isFetching}>
          <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} /> Refresh
        </Button>
        <Button size="sm" onClick={() => setShowCreate(true)}>
          <Plus className="mr-1.5 h-3.5 w-3.5" /> Create Policy
        </Button>
      </div>

      {isLoading ? (
        <div className="flex justify-center py-16">
          <Spinner className="h-6 w-6" />
        </div>
      ) : filtered.length === 0 ? (
        <EmptyState
          icon={<Users className="h-6 w-6" />}
          title="No policies"
          description={
            filter ? "No policies match the filter." : "Create an IAM policy to get started."
          }
          action={
            !filter && (
              <Button size="sm" onClick={() => setShowCreate(true)}>
                <Plus className="mr-1.5 h-3.5 w-3.5" /> Create Policy
              </Button>
            )
          }
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Policy Name</TableHead>
              <TableHead>ARN</TableHead>
              <TableHead>Attachments</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {filtered.map((p) => (
              <TableRow key={p.PolicyName}>
                <TableCell className="font-mono text-sm">{p.PolicyName}</TableCell>
                <TableCell className="text-muted-foreground font-mono text-xs">{p.Arn}</TableCell>
                <TableCell>{p.AttachmentCount}</TableCell>
                <TableCell className="text-right">
                  <Button
                    size="sm"
                    variant="ghost"
                    className="text-danger hover:text-danger"
                    onClick={() => setDeleteTarget({ name: p.PolicyName ?? "", arn: p.Arn ?? "" })}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <CreateResourceDialog
        open={showCreate}
        onOpenChange={setShowCreate}
        title="Create Policy"
        label="Policy Name"
        placeholder="my-policy"
        mutationOptions={createPolicyMutationOptions}
        invalidateKeys={[iamKeys.policies()]}
        successTitle="Policy created"
      />
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(undefined)}
        title="Delete Policy"
        description={
          <>
            Delete policy <span className="font-mono font-semibold">{deleteTarget?.name}</span>?
            This cannot be undone.
          </>
        }
        confirmLabel="Delete"
        variant="danger"
        isPending={deleteMut.isPending}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget.arn)}
      />
    </div>
  )
}

// ─── Groups Tab ────────────────────────────────────────────────────────────

function GroupsTab() {
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string>()
  const [filter, setFilter] = useState("")
  const { data: groups = [], isLoading, isFetching, refetch } = useQuery(iamGroupsQueryOptions())

  const deleteMut = useResourceMutation({
    options: deleteGroupMutationOptions(),
    invalidateKeys: [iamKeys.groups()],
    successTitle: "Group deleted",
    onSuccess: () => setDeleteTarget(undefined),
  })

  const filtered = useMemo(
    () =>
      filter
        ? groups.filter((g) => (g.GroupName ?? "").toLowerCase().includes(filter.toLowerCase()))
        : groups,
    [groups, filter],
  )

  return (
    <div className="flex flex-col gap-3 pt-4">
      <div className="flex items-center gap-2">
        <div className="relative flex-1">
          <Search className="text-muted-foreground absolute top-1/2 left-2 h-3.5 w-3.5 -translate-y-1/2" />
          <Input
            placeholder="Filter groups…"
            className="pl-8"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
        </div>
        <Button size="sm" variant="ghost" onClick={() => refetch()} disabled={isFetching}>
          <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} /> Refresh
        </Button>
        <Button size="sm" onClick={() => setShowCreate(true)}>
          <Plus className="mr-1.5 h-3.5 w-3.5" /> Create Group
        </Button>
      </div>

      {isLoading ? (
        <div className="flex justify-center py-16">
          <Spinner className="h-6 w-6" />
        </div>
      ) : filtered.length === 0 ? (
        <EmptyState
          icon={<Users className="h-6 w-6" />}
          title="No groups"
          description={
            filter ? "No groups match the filter." : "Create an IAM group to get started."
          }
          action={
            !filter && (
              <Button size="sm" onClick={() => setShowCreate(true)}>
                <Plus className="mr-1.5 h-3.5 w-3.5" /> Create Group
              </Button>
            )
          }
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Group Name</TableHead>
              <TableHead>ARN</TableHead>
              <TableHead>Path</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {filtered.map((g) => (
              <TableRow key={g.GroupName}>
                <TableCell className="font-mono text-sm">{g.GroupName}</TableCell>
                <TableCell className="text-muted-foreground font-mono text-xs">{g.Arn}</TableCell>
                <TableCell>{g.Path}</TableCell>
                <TableCell className="text-right">
                  <Button
                    size="sm"
                    variant="ghost"
                    className="text-danger hover:text-danger"
                    onClick={() => setDeleteTarget(g.GroupName)}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <CreateResourceDialog
        open={showCreate}
        onOpenChange={setShowCreate}
        title="Create Group"
        label="Group Name"
        placeholder="my-group"
        mutationOptions={createGroupMutationOptions}
        invalidateKeys={[iamKeys.groups()]}
        successTitle="Group created"
      />
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(undefined)}
        title="Delete Group"
        description={
          <>
            Delete group <span className="font-mono font-semibold">{deleteTarget}</span>? This
            cannot be undone.
          </>
        }
        confirmLabel="Delete"
        variant="danger"
        isPending={deleteMut.isPending}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget)}
      />
    </div>
  )
}
