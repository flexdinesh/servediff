import { Toggle as TogglePrimitive } from "@base-ui/react/toggle";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "cn";

const toggleVariants = cva(
  "group/toggle inline-flex items-center justify-center gap-[var(--space-1)] whitespace-nowrap rounded-[var(--radius-control)] text-[length:var(--text-sm)] font-[var(--weight-medium)] text-[var(--text-secondary)] hover:bg-muted hover:text-foreground disabled:pointer-events-none disabled:opacity-60 aria-invalid:border-destructive aria-pressed:bg-accent aria-pressed:text-accent-foreground data-[state=on]:bg-accent data-[state=on]:text-accent-foreground [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-(--icon-base)",
  {
    variants: {
      variant: {
        default: "bg-transparent",
        outline: "border border-input bg-background hover:bg-muted",
      },
      size: {
        default:
          "h-[var(--control-height)] min-w-[var(--control-height)] px-[var(--space-2)]",
        sm: "h-[var(--control-compact)] min-w-[var(--control-compact)] px-[var(--space-2)] text-[length:var(--text-sm)] [&_svg:not([class*='size-'])]:size-(--icon-sm)",
        lg: "h-9 min-w-9 px-2.5 has-data-[icon=inline-end]:pr-[var(--space-2)] has-data-[icon=inline-start]:pl-[var(--space-2)]",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "default",
    },
  },
);

function Toggle({
  className,
  variant = "default",
  size = "default",
  ...props
}: TogglePrimitive.Props & VariantProps<typeof toggleVariants>) {
  return (
    <TogglePrimitive
      data-slot="toggle"
      className={cn(toggleVariants({ variant, size, className }))}
      {...props}
    />
  );
}

export { Toggle, toggleVariants };
