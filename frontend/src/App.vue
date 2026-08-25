<template>
  <RouterView />
  <ToastStack />
  <ConfirmDialog />
  <TooltipLayer />
</template>

<script setup>
import ConfirmDialog from './components/ConfirmDialog.vue'
import ToastStack from './components/ToastStack.vue'
import TooltipLayer from './components/TooltipLayer.vue'
</script>
