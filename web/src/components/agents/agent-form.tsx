"use client";

import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";

interface AgentFormData {
  name: string;
  slug: string;
  description: string;
  instruction: string;
  model_config: {
    backend: string;
    model: string;
    temperature: number;
    maxTokens: number;
  };
  tools: string[];
  visibility: string;
  category: string;
}

interface AgentFormProps {
  initialData?: Partial<AgentFormData>;
  onSubmit: (data: AgentFormData) => Promise<void>;
  submitLabel: string;
}

const defaultData: AgentFormData = {
  name: "",
  slug: "",
  description: "",
  instruction: "",
  model_config: {
    backend: "claude",
    model: "claude-sonnet-4-20250514",
    temperature: 0.7,
    maxTokens: 8192,
  },
  tools: [],
  visibility: "private",
  category: "",
};

const claudeModels = [
  "claude-sonnet-4-20250514",
  "claude-opus-4-20250514",
];

export function AgentForm({ initialData, onSubmit, submitLabel }: AgentFormProps) {
  const [data, setData] = useState<AgentFormData>({ ...defaultData, ...initialData });
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    setError(null);
    try {
      await onSubmit(data);
    } catch (err: any) {
      setError(err.message || "An error occurred");
    } finally {
      setLoading(false);
    }
  };

  const updateField = <K extends keyof AgentFormData>(key: K, value: AgentFormData[K]) => {
    setData((prev) => ({ ...prev, [key]: value }));
  };

  const updateModelConfig = (key: string, value: string | number) => {
    setData((prev) => ({
      ...prev,
      model_config: { ...prev.model_config, [key]: value },
    }));
  };

  // Auto-generate slug from name
  const handleNameChange = (name: string) => {
    updateField("name", name);
    if (!initialData?.slug) {
      const slug = name
        .toLowerCase()
        .replace(/[^a-z0-9]+/g, "-")
        .replace(/^-|-$/g, "");
      updateField("slug", slug);
    }
  };

  return (
    <form onSubmit={handleSubmit} className="space-y-6">
      {error && (
        <div className="rounded-md bg-destructive/10 p-3 text-sm text-destructive">{error}</div>
      )}

      <div className="grid gap-4 sm:grid-cols-2">
        <div className="space-y-2">
          <Label htmlFor="name">Name</Label>
          <Input
            id="name"
            value={data.name}
            onChange={(e) => handleNameChange(e.target.value)}
            placeholder="My Agent"
            required
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="slug">Slug</Label>
          <Input
            id="slug"
            value={data.slug}
            onChange={(e) => updateField("slug", e.target.value)}
            placeholder="my-agent"
            pattern="[a-z0-9][a-z0-9\-]*[a-z0-9]"
            required
          />
        </div>
      </div>

      <div className="space-y-2">
        <Label htmlFor="description">Description</Label>
        <Input
          id="description"
          value={data.description}
          onChange={(e) => updateField("description", e.target.value)}
          placeholder="A helpful agent that..."
        />
      </div>

      <div className="space-y-2">
        <Label htmlFor="instruction">System Instruction</Label>
        <Textarea
          id="instruction"
          value={data.instruction}
          onChange={(e) => updateField("instruction", e.target.value)}
          placeholder="You are a helpful assistant..."
          rows={8}
          required
        />
      </div>

      <fieldset className="space-y-4 rounded-lg border p-4">
        <legend className="px-2 text-sm font-medium">Model Configuration</legend>
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-2">
            <Label htmlFor="backend">Backend</Label>
            <Select
              id="backend"
              value={data.model_config.backend}
              onChange={(e) => updateModelConfig("backend", e.target.value)}
            >
              <option value="claude">Claude API</option>
              <option value="ollama">Ollama (Local)</option>
              <option value="vllm">vLLM (Local)</option>
            </Select>
          </div>
          <div className="space-y-2">
            <Label htmlFor="model">Model</Label>
            {data.model_config.backend === "claude" ? (
              <Select
                id="model"
                value={data.model_config.model}
                onChange={(e) => updateModelConfig("model", e.target.value)}
              >
                {claudeModels.map((m) => (
                  <option key={m} value={m}>{m}</option>
                ))}
              </Select>
            ) : (
              <Input
                id="model"
                value={data.model_config.model}
                onChange={(e) => updateModelConfig("model", e.target.value)}
                placeholder={data.model_config.backend === "ollama" ? "llama3.3:70b" : "meta-llama/Llama-3.3-70B-Instruct"}
              />
            )}
          </div>
          <div className="space-y-2">
            <Label htmlFor="temperature">Temperature</Label>
            <Input
              id="temperature"
              type="number"
              min={0}
              max={2}
              step={0.1}
              value={data.model_config.temperature}
              onChange={(e) => updateModelConfig("temperature", parseFloat(e.target.value))}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="maxTokens">Max Tokens</Label>
            <Input
              id="maxTokens"
              type="number"
              min={1}
              max={200000}
              value={data.model_config.maxTokens}
              onChange={(e) => updateModelConfig("maxTokens", parseInt(e.target.value))}
            />
          </div>
        </div>
      </fieldset>

      <div className="grid gap-4 sm:grid-cols-2">
        <div className="space-y-2">
          <Label htmlFor="visibility">Visibility</Label>
          <Select
            id="visibility"
            value={data.visibility}
            onChange={(e) => updateField("visibility", e.target.value)}
          >
            <option value="private">Private</option>
            <option value="team">Team</option>
            <option value="public">Public</option>
          </Select>
        </div>
        <div className="space-y-2">
          <Label htmlFor="category">Category</Label>
          <Input
            id="category"
            value={data.category}
            onChange={(e) => updateField("category", e.target.value)}
            placeholder="coding, writing, research..."
          />
        </div>
      </div>

      <div className="flex justify-end gap-3">
        <Button type="submit" disabled={loading}>
          {loading ? "Saving..." : submitLabel}
        </Button>
      </div>
    </form>
  );
}
