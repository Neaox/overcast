export const RETRY_STATUSES = ["fail", "skip", "unimplemented"] as const;

export const PLANNED_SUITES: {
  id: string;
  label: string;
  sublabel: string;
  category: "sdk" | "iac";
}[] = [
  { id: "node-js-sdk", label: "Node.js", sublabel: "SDK v3", category: "sdk" },
  { id: "python-sdk", label: "Python", sublabel: "boto3", category: "sdk" },
  { id: "go-sdk", label: "Go", sublabel: "SDK v2", category: "sdk" },
  { id: "java-sdk", label: "Java", sublabel: "SDK v2", category: "sdk" },
  { id: "dotnet-sdk", label: ".NET", sublabel: "SDK v3", category: "sdk" },
  { id: "rust-sdk", label: "Rust", sublabel: "SDK", category: "sdk" },
  { id: "cli", label: "AWS CLI", sublabel: "v2", category: "sdk" },
  { id: "cdk", label: "CDK", sublabel: "TypeScript v2", category: "iac" },
  { id: "tofu", label: "OpenTofu", sublabel: "AWS provider", category: "iac" },
  {
    id: "terraform",
    label: "Terraform",
    sublabel: "AWS provider",
    category: "iac",
  },
  { id: "pulumi", label: "Pulumi", sublabel: "AWS provider", category: "iac" },
];
