import * as React from "react"
import { cn } from "@/lib/utils"

function Card({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn("rounded-lg border border-border bg-bg-elevated text-fg shadow-sm", className)}
      {...props}
    />
  )
}

function CardHeader({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("flex flex-col gap-1.5 p-4 pb-0", className)} {...props} />
}

function CardTitle({ className, ...props }: React.HTMLAttributes<HTMLHeadingElement>) {
  return (
    <h3 className={cn("text-sm leading-none font-semibold tracking-tight", className)} {...props} />
  )
}

function CardDescription({ className, ...props }: React.HTMLAttributes<HTMLParagraphElement>) {
  return <p className={cn("text-sm text-fg-muted", className)} {...props} />
}

function CardContent({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("p-4", className)} {...props} />
}

function CardFooter({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("flex items-center p-4 pt-0", className)} {...props} />
}

export { Card, CardHeader, CardTitle, CardDescription, CardContent, CardFooter }
