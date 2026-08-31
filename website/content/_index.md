---
title: nem
layout: hextra-home
---

<div>
{{< hextra/hero-container class="nem-hero"
  image="images/nem-mark.svg" imageTitle="nem" imageWidth="120" imageHeight="120" >}}
    <div>
      <h1 class="hx:font-bold">nem</h1>
    </div>
{{< /hextra/hero-container >}}
</div>

{{< hextra/hero-badge class="hx:mt-6" >}}
  <div class="hx:w-2 hx:h-2 hx:rounded-full hx:bg-primary-400"></div>
  <span>Free, open source</span>
{{< /hextra/hero-badge >}}

<div class="hx:mt-6">
{{< hextra/hero-headline >}}
  Reproducible development environments.
{{< /hextra/hero-headline >}}
</div>

<div class="hx:mt-2">
{{< hextra/hero-subtitle >}}
  Your devtools and environment variables, exactly as you need, wherever you need them.
{{< /hextra/hero-subtitle >}}
</div>

<div class="hx:mt-12">
{{< hextra/hero-button text="Get Started" link="docs/getting-started/" >}}
</div>

<div class="hx:mt-12">
{{< hextra/feature-grid >}}
  {{< hextra/feature-card
    title="Devtools as dependencies"
    subtitle="<br/>Each project gets its own tool versions, switched automatically as you change directories — kubectl, terraform, go, or anything else. One `nem.toml` covers tools that would otherwise each bring their own version manager." >}}

  {{< hextra/feature-card
    title="Same environment everywhere"
    subtitle="<br/>Commit `nem.lock` and pipelines, containers, and AI coding agents install exactly the same tools — same versions, same digests. Rootless container images drop nem into CI jobs and dev containers cleanly." >}}

  {{< hextra/feature-card
    title="Works behind firewall"
    subtitle="<br/>Host catalogs and packages with your own OCI registry, and audit them like any other artifact. Installs are checksum-verified and never need root — fit for regulated and air-gapped networks." >}}
{{< /hextra/feature-grid >}}
</div>
