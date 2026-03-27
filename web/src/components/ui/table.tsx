import * as React from "react"
import { cn } from "@/lib/utils"

function Table({ className, ...props }: React.HTMLAttributes<HTMLTableElement>) {
  return (
    <div className="relative w-full overflow-auto">
      <table className={cn("w-full caption-bottom text-sm", className)} {...props} />
    </div>
  )
}

function TableHeader({ className, ...props }: React.HTMLAttributes<HTMLTableSectionElement>) {
  return <thead className={cn("border-b border-border", className)} {...props} />
}

function TableBody({ className, ...props }: React.HTMLAttributes<HTMLTableSectionElement>) {
  return <tbody className={cn("[&_tr:last-child]:border-0", className)} {...props} />
}

function TableRow({ className, ...props }: React.HTMLAttributes<HTMLTableRowElement>) {
  return (
    <tr
      className={cn(
        "cursor-pointer border-b border-border-muted transition-colors hover:bg-bg-subtle",
        "data-[selected=true]:bg-accent-muted",
        className,
      )}
      {...props}
    />
  )
}

function TableHead({ className, ...props }: React.ThHTMLAttributes<HTMLTableCellElement>) {
  return (
    <th
      className={cn(
        "h-9 px-3 text-left align-middle text-sm font-medium text-fg-muted",
        "whitespace-nowrap",
        className,
      )}
      {...props}
    />
  )
}

function TableCell({ className, ...props }: React.TdHTMLAttributes<HTMLTableCellElement>) {
  return <td className={cn("px-3 py-2 align-middle text-sm text-fg", className)} {...props} />
}

function TableEmpty({ children, colSpan = 99 }: { children: React.ReactNode; colSpan?: number }) {
  return (
    <tr>
      <td colSpan={colSpan} className="py-12 text-center text-sm text-fg-muted">
        {children}
      </td>
    </tr>
  )
}

export { Table, TableHeader, TableBody, TableRow, TableHead, TableCell, TableEmpty }
