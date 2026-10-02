import * as Menu from "@radix-ui/react-dropdown-menu";
import { DotsThree } from "@phosphor-icons/react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { tapIcon } from "./parts";

export type MenuAction =
  | {
      label: string;
      onSelect: () => void;
      disabled?: boolean;
      destructive?: boolean;
    }
  | "separator";

/**
 * Overflow menu for the less frequent actions on a row. Disabled items stay
 * listed (greyed) so the full set of actions is always discoverable.
 */
export function ActionMenu({
  label,
  actions,
}: {
  label: string;
  actions: MenuAction[];
}) {
  return (
    <Menu.Root modal={false}>
      <Menu.Trigger asChild>
        <Button variant="ghost" size="icon-sm" className={tapIcon} aria-label={label}>
          <DotsThree weight="bold" />
        </Button>
      </Menu.Trigger>
      <Menu.Portal>
        <Menu.Content
          align="end"
          sideOffset={4}
          className="m-open z-50 min-w-44 rounded-[var(--radius-lg)] border border-border bg-popover p-1 text-sm text-popover-foreground shadow-[0_10px_28px_-14px_#00000080]"
        >
          {actions.map((a, i) =>
            a === "separator" ? (
              <Menu.Separator key={`sep-${i}`} className="my-1 h-px bg-border" />
            ) : (
              <Menu.Item
                key={a.label}
                disabled={a.disabled}
                onSelect={a.onSelect}
                className={cn(
                  "flex min-h-11 cursor-default select-none items-center rounded-[var(--radius-sm)] px-2.5 outline-none lg:min-h-8",
                  "data-[highlighted]:bg-raised data-[highlighted]:text-foreground",
                  "data-[disabled]:pointer-events-none data-[disabled]:opacity-50",
                  a.destructive && "text-destructive data-[highlighted]:text-destructive",
                )}
              >
                {a.label}
              </Menu.Item>
            ),
          )}
        </Menu.Content>
      </Menu.Portal>
    </Menu.Root>
  );
}
