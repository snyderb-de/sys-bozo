const current = location.pathname.split("/").pop() || "index.html";

document.querySelectorAll(".nav a").forEach((link) => {
  const href = link.getAttribute("href");
  if (href === current || (current === "" && href === "index.html")) {
    link.setAttribute("aria-current", "page");
  }
});

document.querySelectorAll("[data-updated]").forEach((node) => {
  node.textContent = new Date().toISOString().slice(0, 10);
});
