import { useNavigate } from "react-router";
import { api } from "../api/client.ts";
import type { Project } from "../api/types.ts";
import { named } from "../lib/form.ts";
import { label } from "../lib/format.ts";
import { queryClient } from "../query.ts";
import { Dialog } from "../ui/Dialog.tsx";
import { NameAndSlug } from "../ui/fields.tsx";
import { toast } from "../ui/toast.ts";

// NewProject is the dialog that creates a project, and then opens it.
export function NewProject({ onClose }: { onClose: () => void }) {
  const navigate = useNavigate();
  return (
    <Dialog
      title="New project"
      action="Create project"
      onSubmit={async (data) => {
        const project = await api.post<Project>("/v1/projects", named(data));
        await queryClient.invalidateQueries({ queryKey: ["projects"] });
        return project;
      }}
      onClose={(created) => {
        onClose();
        if (!created) return;
        toast(`Created ${label(created)}`, "ok");
        void navigate(`/p/${created.slug}`);
      }}
    >
      <NameAndSlug example="Storefront" />
    </Dialog>
  );
}
