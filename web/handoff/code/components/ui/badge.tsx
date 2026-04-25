import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

const badgeVariants = cva(
  "inline-flex items-center gap-1 h-[22px] px-2 rounded-[5px] text-[11.5px] font-medium border",
  {
    variants: {
      variant: {
        default: "bg-hover text-fg-secondary border-transparent",
        secondary: "bg-hover text-fg-secondary border-transparent",
        outline: "bg-transparent text-fg-secondary border-border",
        accent: "bg-accent-soft text-accent border-transparent",
        destructive: "bg-status-error-soft text-status-error border-transparent",
      },
    },
    defaultVariants: { variant: "default" },
  }
);

export interface BadgeProps
  extends React.HTMLAttributes<HTMLSpanElement>,
    VariantProps<typeof badgeVariants> {}

export function Badge({ className, variant, ...props }: BadgeProps) {
  return <span className={cn(badgeVariants({ variant }), className)} {...props} />;
}

export { badgeVariants };
