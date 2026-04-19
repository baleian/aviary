"use client";

import {
  DndContext,
  KeyboardSensor,
  PointerSensor,
  closestCenter,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import {
  SortableContext,
  arrayMove,
  horizontalListSortingStrategy,
  sortableKeyboardCoordinates,
  useSortable,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { PanelRightClose, X } from "@/components/icons";
import { cn } from "@/lib/utils";
import type { EditorTab } from "../hooks/use-workspace-editor";
import { basename, sandboxPath } from "../lib/paths";

interface EditorTabsProps {
  tabs: EditorTab[];
  activeTabPath: string | null;
  onActivate: (path: string) => void;
  onClose: (path: string) => void;
  onPin: (path: string) => void;
  onContextMenu: (e: React.MouseEvent, path: string) => void;
  onReorder: (orderedPaths: string[]) => void;
  onCollapseEditor: () => void;
}

export function EditorTabs({
  tabs, activeTabPath, onActivate, onClose, onPin, onContextMenu, onReorder, onCollapseEditor,
}: EditorTabsProps) {
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  const handleDragEnd = (e: DragEndEvent) => {
    const { active, over } = e;
    if (!over || active.id === over.id) return;
    const ids = tabs.map((t) => t.path);
    const oldIdx = ids.indexOf(active.id as string);
    const newIdx = ids.indexOf(over.id as string);
    if (oldIdx === -1 || newIdx === -1) return;
    onReorder(arrayMove(ids, oldIdx, newIdx));
  };

  return (
    <div className="flex shrink-0 items-stretch border-b border-white/[0.06] bg-elevated">
      <div className="flex min-w-0 flex-1 items-stretch overflow-x-auto">
        <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleDragEnd}>
          <SortableContext items={tabs.map((t) => t.path)} strategy={horizontalListSortingStrategy}>
            {tabs.map((tab) => (
              <SortableTab
                key={tab.path}
                tab={tab}
                active={tab.path === activeTabPath}
                onActivate={onActivate}
                onClose={onClose}
                onPin={onPin}
                onContextMenu={onContextMenu}
              />
            ))}
          </SortableContext>
        </DndContext>
      </div>
      <button
        type="button"
        onClick={onCollapseEditor}
        aria-label="Collapse editor"
        title="Collapse editor"
        className="flex h-auto w-8 shrink-0 items-center justify-center border-l border-white/[0.06] text-fg-muted hover:bg-raised hover:text-fg-primary"
      >
        <PanelRightClose size={14} strokeWidth={2} />
      </button>
    </div>
  );
}

interface SortableTabProps {
  tab: EditorTab;
  active: boolean;
  onActivate: (path: string) => void;
  onClose: (path: string) => void;
  onPin: (path: string) => void;
  onContextMenu: (e: React.MouseEvent, path: string) => void;
}

function SortableTab({ tab, active, onActivate, onClose, onPin, onContextMenu }: SortableTabProps) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: tab.path,
  });
  const dirty = tab.draft !== null;
  const preview = !tab.pinned;
  const style: React.CSSProperties = {
    transform: CSS.Transform.toString(transform),
    transition,
    zIndex: isDragging ? 10 : undefined,
  };

  return (
    <div
      ref={setNodeRef}
      style={style}
      {...attributes}
      {...listeners}
      role="tab"
      aria-selected={active}
      tabIndex={0}
      onClick={() => onActivate(tab.path)}
      onDoubleClick={() => onPin(tab.path)}
      onContextMenu={(e) => {
        e.preventDefault();
        onContextMenu(e, tab.path);
      }}
      onAuxClick={(e) => {
        if (e.button === 1) {
          e.preventDefault();
          onClose(tab.path);
        }
      }}
      onDragStart={(e) => e.preventDefault()}
      className={cn(
        "group flex min-w-0 cursor-pointer items-center gap-1.5 border-r border-white/[0.06] px-3 py-1.5 type-caption transition-colors",
        active
          ? "bg-canvas text-fg-primary"
          : "text-fg-muted hover:bg-raised hover:text-fg-primary",
        isDragging && "opacity-50",
      )}
      title={sandboxPath(tab.path)}
    >
      <span
        className={cn(
          "truncate max-w-[160px] font-mono",
          preview && "italic",
        )}
      >
        {basename(tab.path)}
      </span>
      {dirty && (
        <span
          aria-label="Unsaved changes"
          className="h-1.5 w-1.5 shrink-0 rounded-full bg-fg-primary"
        />
      )}
      <button
        type="button"
        onPointerDown={(e) => e.stopPropagation()}
        onClick={(e) => {
          e.stopPropagation();
          onClose(tab.path);
        }}
        aria-label={`Close ${basename(tab.path)}`}
        className="flex h-4 w-4 shrink-0 items-center justify-center rounded-xs text-fg-muted hover:bg-white/10 hover:text-fg-primary"
      >
        <X size={10} strokeWidth={2.5} />
      </button>
    </div>
  );
}
