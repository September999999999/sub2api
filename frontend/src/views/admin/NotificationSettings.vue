<script setup lang="ts">
import { ref, reactive, onMounted, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import {
  getWebhookConfig,
  updateWebhookConfig,
  getPlatforms,
  addPlatform,
  updatePlatform,
  deletePlatform,
  togglePlatform,
  testPlatform as apiTestPlatform,
  testNotification,
  type WebhookConfig,
  type PlatformConfig,
  type WebhookState
} from '@/api/notification'


import AppLayout from '@/components/layout/AppLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Toggle from '@/components/common/Toggle.vue'
import Select from '@/components/common/Select.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import type { Column } from '@/components/common/types'

const { t } = useI18n()
const appStore = useAppStore()

const showWebhookStateIssue = (state: WebhookState, fallbackMessage: string) => {
  if (state.status === 'ok') return
  const issue = state.issues?.[0]
  appStore.showError(issue?.message || fallbackMessage)
}

const loading = ref(true)
const config = ref<WebhookConfig | null>(null)

const platforms = ref<PlatformConfig[]>([])
const platformColumns = computed<Column[]>(() => [
  { key: 'name', label: t('admin.notifications.platformName') },
  { key: 'type', label: t('admin.notifications.platformType') },
  { key: 'enabled', label: t('common.enabled') },
  { key: 'actions', label: t('common.actions'), class: 'text-right' }
])

const showPlatformModal = ref(false)
const isEditing = ref(false)
const platformForm = reactive<Partial<PlatformConfig>>({})
const platformTypes = ['dingtalk', 'discord', 'telegram', 'bark', 'custom', 'smtp'] as const
const platformTypeOptions = platformTypes.map(type => ({ label: type, value: type }))

const resetPlatformForm = () => {
  Object.keys(platformForm).forEach((key) => {
    delete (platformForm as Record<string, unknown>)[key]
  })
}

const testMessage = reactive({
  type: 'systemError',
  title: 'Test Notification',
  content: 'This is a test notification from sub2api.'
})

const showDeleteConfirm = ref(false)
const deletingPlatformId = ref<string | null>(null)

const loadConfig = async () => {
  try {
    const resp = await getWebhookConfig()
    config.value = resp.config
    showWebhookStateIssue(resp.state, t('admin.notifications.loadConfigError'))
  } catch {
    appStore.showError(t('admin.notifications.loadConfigError'))
  }
}

const loadPlatforms = async () => {
  loading.value = true
  try {
    const resp = await getPlatforms()
    platforms.value = resp.items
    showWebhookStateIssue(resp.state, t('admin.notifications.loadPlatformsError'))
  } catch {
    appStore.showError(t('admin.notifications.loadPlatformsError'))
  } finally {
    loading.value = false
  }
}

const saveConfig = async () => {
  if (!config.value) return
  try {
    config.value = await updateWebhookConfig(config.value)
    appStore.showSuccess(t('admin.notifications.saveSuccess'))
  } catch {
    appStore.showError(t('admin.notifications.saveError'))
  }
}

const openPlatformModal = (platform?: PlatformConfig) => {
  resetPlatformForm()
  if (platform) {
    isEditing.value = true
    Object.assign(platformForm, platform)
  } else {
    isEditing.value = false
    Object.assign(platformForm, {
      name: '',
      type: 'custom',
      enabled: true
    })
  }
  showPlatformModal.value = true
}

const closePlatformModal = () => {
  showPlatformModal.value = false
}

const savePlatform = async () => {
  try {
    if (isEditing.value && platformForm.id) {
      await updatePlatform(platformForm.id, platformForm)
    } else {
      await addPlatform(platformForm as Omit<PlatformConfig, 'id'>)
    }
    appStore.showSuccess(t('admin.notifications.savePlatformSuccess'))
    closePlatformModal()
    await loadPlatforms()
  } catch {
    appStore.showError(t('admin.notifications.savePlatformError'))
  }
}

const confirmDeletePlatform = (platform: PlatformConfig) => {
  deletingPlatformId.value = platform.id
  showDeleteConfirm.value = true
}

const deleteConfirmed = async () => {
  if (deletingPlatformId.value) {
    try {
      await deletePlatform(deletingPlatformId.value)
      appStore.showSuccess(t('admin.notifications.deleteSuccess'))
      await loadPlatforms()
    } catch {
      appStore.showError(t('admin.notifications.deleteError'))
    } finally {
      showDeleteConfirm.value = false
      deletingPlatformId.value = null
    }
  }
}

const togglePlatformEnabled = async (id: string) => {
  try {
    await togglePlatform(id)
    appStore.showSuccess(t('admin.notifications.toggleSuccess'))
    await loadPlatforms()
  } catch {
    appStore.showError(t('admin.notifications.toggleError'))
  }
}

const handleTestPlatform = async (id: string) => {
  try {
    await apiTestPlatform(id)
    appStore.showSuccess(t('admin.notifications.testSuccess'))
  } catch {
    appStore.showError(t('admin.notifications.testError'))
  }
}

const sendTestNotification = async () => {
  try {
    await testNotification(testMessage)
    appStore.showSuccess(t('admin.notifications.testSent'))
  } catch {
    appStore.showError(t('admin.notifications.testSendError'))
  }
}

onMounted(async () => {
  await Promise.all([loadConfig(), loadPlatforms()])
})

</script>

<template>
  <AppLayout>
    <div class="space-y-6">
      <!-- Main settings grid -->
      <div class="grid grid-cols-1 lg:grid-cols-3 gap-6">
        <!-- Left column for primary settings -->
        <div class="lg:col-span-2 space-y-6">
          <!-- Platforms Card -->
          <div class="card p-4">
            <div class="flex justify-between items-center mb-4">
              <h2 class="text-lg font-semibold">{{ t('admin.notifications.platforms') }}</h2>
              <button @click="openPlatformModal()" class="flex items-center gap-2 px-4 py-2 text-sm font-medium rounded-lg bg-primary-600 text-white hover:bg-primary-700 transition-colors duration-150">
                <svg class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M12 4.5v15m7.5-7.5h-15" />
                </svg>
                {{ t('admin.notifications.addPlatform') }}
              </button>
            </div>
            <DataTable :columns="platformColumns" :data="platforms" :loading="loading">
              <template #cell-actions="{ row }">
                <div class="flex items-center justify-end gap-2">
                  <button @click="handleTestPlatform(row.id)" class="p-2 rounded-md hover:bg-gray-100 dark:hover:bg-dark-700" :title="t('common.test')">
                    <svg class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M5.25 5.653c0-.856.917-1.398 1.667-.986l11.54 6.348a1.125 1.125 0 010 1.971l-11.54 6.347a1.125 1.125 0 01-1.667-.985V5.653z" />
                    </svg>
                  </button>
                  <button @click="openPlatformModal(row)" class="p-2 rounded-md hover:bg-gray-100 dark:hover:bg-dark-700" :title="t('common.edit')">
                      <svg class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M16.862 4.487l1.687-1.688a1.875 1.875 0 112.652 2.652L10.582 16.07a4.5 4.5 0 01-1.897 1.13L6 18l.8-2.685a4.5 4.5 0 011.13-1.897l8.932-8.931zm0 0L19.5 7.125M18 14v4.75A2.25 2.25 0 0115.75 21H5.25A2.25 2.25 0 013 18.75V8.25A2.25 2.25 0 015.25 6H10" />
                    </svg>
                  </button>
                  <button @click="confirmDeletePlatform(row)" class="p-2 rounded-md text-red-500 hover:bg-red-100 dark:hover:bg-red-900/30" :title="t('common.delete')">
                      <svg class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M14.74 9l-.346 9m-4.788 0L9.26 9m9.968-3.21c.342.052.682.107 1.022.166m-1.022-.165L18.16 19.673a2.25 2.25 0 01-2.244 2.077H8.084a2.25 2.25 0 01-2.244-2.077L4.772 5.79m14.456 0a48.108 48.108 0 00-3.478-.397m-12 .562c.34-.059.68-.114 1.022-.165m0 0a48.11 48.11 0 013.478-.397m7.5 0v-.916c0-1.18-.91-2.164-2.09-2.201a51.964 51.964 0 00-3.32 0c-1.18.037-2.09 1.022-2.09 2.201v.916m7.5 0a48.667 48.667 0 00-7.5 0" />
                    </svg>
                  </button>
                </div>
              </template>
               <template #cell-enabled="{ row }">
                <Toggle :modelValue="row.enabled" @update:modelValue="togglePlatformEnabled(row.id)" />
              </template>
            </DataTable>
          </div>
          

        </div>

        <!-- Right column for auxiliary settings -->
        <div class="space-y-6">
          <!-- General Settings Card -->
          <div class="card p-4">
            <h2 class="text-lg font-semibold mb-4">{{ t('admin.notifications.generalSettings') }}</h2>
            <div class="flex items-center justify-between">
              <div>
                <h3 class="font-medium text-gray-900 dark:text-white">{{ t('admin.notifications.enableNotifications') }}</h3>
                <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.notifications.enableNotificationsHint') }}</p>
              </div>
              <Toggle v-if="config" v-model="config.enabled" @update:modelValue="saveConfig" />
            </div>
          </div>

          <!-- Retry Settings Card -->
          <div class="card p-4">
            <h2 class="text-lg font-semibold mb-4">{{ t('admin.notifications.retrySettings') }}</h2>
            <div v-if="config && config.retrySettings" class="space-y-4">
              <div>
                <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">{{ t('admin.notifications.maxRetries') }}</label>
                <input type="number" v-model.number="config.retrySettings.maxRetries" @change="saveConfig" class="w-full px-3 py-2 text-sm rounded-md bg-gray-50 dark:bg-dark-700 border border-gray-200 dark:border-dark-600 focus:outline-none focus:ring-2 focus:ring-primary-500/30 focus:border-primary-500">
              </div>
              <div>
                <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">{{ t('admin.notifications.retryDelay') }}</label>
                <input type="number" v-model.number="config.retrySettings.retryDelay" @change="saveConfig" class="w-full px-3 py-2 text-sm rounded-md bg-gray-50 dark:bg-dark-700 border border-gray-200 dark:border-dark-600 focus:outline-none focus:ring-2 focus:ring-primary-500/30 focus:border-primary-500">
              </div>
              <div>
                <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">{{ t('admin.notifications.timeout') }}</label>
                <input type="number" v-model.number="config.retrySettings.timeout" @change="saveConfig" class="w-full px-3 py-2 text-sm rounded-md bg-gray-50 dark:bg-dark-700 border border-gray-200 dark:border-dark-600 focus:outline-none focus:ring-2 focus:ring-primary-500/30 focus:border-primary-500">
              </div>
            </div>
          </div>

          <!-- Test Notification Card -->
          <div class="card p-4">
            <h2 class="text-lg font-semibold mb-4">{{ t('admin.notifications.testNotification') }}</h2>
            <div class="space-y-4">
              <input type="text" v-model="testMessage.type" :placeholder="t('admin.notifications.platformType')" class="w-full px-3 py-2 text-sm rounded-md bg-gray-50 dark:bg-dark-700 border border-gray-200 dark:border-dark-600 focus:outline-none focus:ring-2 focus:ring-primary-500/30 focus:border-primary-500">
              <input type="text" v-model="testMessage.title" :placeholder="t('admin.notifications.testTitle')" class="w-full px-3 py-2 text-sm rounded-md bg-gray-50 dark:bg-dark-700 border border-gray-200 dark:border-dark-600 focus:outline-none focus:ring-2 focus:ring-primary-500/30 focus:border-primary-500">
              <textarea v-model="testMessage.content" :placeholder="t('admin.notifications.testContent')" class="w-full px-3 py-2 text-sm rounded-md bg-gray-50 dark:bg-dark-700 border border-gray-200 dark:border-dark-600 focus:outline-none focus:ring-2 focus:ring-primary-500/30 focus:border-primary-500" rows="3"></textarea>
              <button @click="sendTestNotification" class="w-full px-4 py-2 text-sm font-medium rounded-lg border border-gray-200 dark:border-dark-600 hover:bg-gray-100 dark:hover:bg-dark-700 transition-colors duration-150">{{ t('admin.notifications.sendTest') }}</button>
            </div>
          </div>
        </div>
      </div>
    </div>
    
    <!-- Platform Modal -->
    <BaseDialog :show="showPlatformModal" :title="isEditing ? t('admin.notifications.editPlatform') : t('admin.notifications.addPlatform')" @close="closePlatformModal">
      <form @submit.prevent="savePlatform" class="space-y-4">
        <div>
          <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">{{ t('admin.notifications.platformName') }}</label>
          <input type="text" v-model="platformForm.name" required class="w-full px-3 py-2 text-sm rounded-md bg-gray-50 dark:bg-dark-700 border border-gray-200 dark:border-dark-600 focus:outline-none focus:ring-2 focus:ring-primary-500/30 focus:border-primary-500">
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">{{ t('admin.notifications.platformType') }}</label>
           <Select v-model="platformForm.type" :options="platformTypeOptions" />
        </div>
        <!-- Conditional Fields based on platform type -->
        <div v-if="platformForm.type && ['discord', 'dingtalk', 'custom'].includes(platformForm.type as string)">
          <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">{{ t('admin.notifications.webhookUrl') }}</label>
          <input type="url" v-model="platformForm.url" class="w-full px-3 py-2 text-sm rounded-md bg-gray-50 dark:bg-dark-700 border border-gray-200 dark:border-dark-600 focus:outline-none focus:ring-2 focus:ring-primary-500/30 focus:border-primary-500" required>
        </div>

        <div v-if="platformForm.type && platformForm.type === 'bark'">
          <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">{{ t('admin.notifications.token') }}</label>
          <input type="text" v-model="platformForm.token" class="w-full px-3 py-2 text-sm rounded-md bg-gray-50 dark:bg-dark-700 border border-gray-200 dark:border-dark-600 focus:outline-none focus:ring-2 focus:ring-primary-500/30 focus:border-primary-500" :placeholder="t('admin.notifications.barkDeviceKeyHint')" required>
          <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.notifications.barkUrlHint') }}</p>
          <input type="url" v-model="platformForm.url" class="mt-2 w-full px-3 py-2 text-sm rounded-md bg-gray-50 dark:bg-dark-700 border border-gray-200 dark:border-dark-600 focus:outline-none focus:ring-2 focus:ring-primary-500/30 focus:border-primary-500">
        </div>

        <div v-if="platformForm.type && platformForm.type === 'telegram'" class="space-y-4">
          <div>
            <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">{{ t('admin.notifications.telegram.botToken') }}</label>
            <input type="text" v-model="platformForm.botToken" class="w-full px-3 py-2 text-sm rounded-md bg-gray-50 dark:bg-dark-700 border border-gray-200 dark:border-dark-600 focus:outline-none focus:ring-2 focus:ring-primary-500/30 focus:border-primary-500" required>
          </div>
          <div>
            <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">{{ t('admin.notifications.telegram.chatId') }}</label>
            <input type="text" v-model="platformForm.chatId" class="w-full px-3 py-2 text-sm rounded-md bg-gray-50 dark:bg-dark-700 border border-gray-200 dark:border-dark-600 focus:outline-none focus:ring-2 focus:ring-primary-500/30 focus:border-primary-500" required>
          </div>
          <div>
            <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">{{ t('admin.notifications.telegram.apiBaseUrl') }}</label>
            <input type="url" v-model="platformForm.apiBaseUrl" class="w-full px-3 py-2 text-sm rounded-md bg-gray-50 dark:bg-dark-700 border border-gray-200 dark:border-dark-600 focus:outline-none focus:ring-2 focus:ring-primary-500/30 focus:border-primary-500">
            <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.notifications.telegram.apiBaseUrlHint') }}</p>
          </div>
          <div>
            <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">{{ t('admin.notifications.telegram.proxyUrl') }}</label>
            <input type="text" v-model="platformForm.proxyUrl" class="w-full px-3 py-2 text-sm rounded-md bg-gray-50 dark:bg-dark-700 border border-gray-200 dark:border-dark-600 focus:outline-none focus:ring-2 focus:ring-primary-500/30 focus:border-primary-500">
            <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.notifications.telegram.proxyUrlHint') }}</p>
          </div>
        </div>

        <div v-if="platformForm.type && platformForm.type === 'smtp'" class="space-y-4">
          <div>
            <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">{{ t('admin.notifications.smtp.host') }}</label>
            <input type="text" v-model="platformForm.smtpHost" class="w-full px-3 py-2 text-sm rounded-md bg-gray-50 dark:bg-dark-700 border border-gray-200 dark:border-dark-600 focus:outline-none focus:ring-2 focus:ring-primary-500/30 focus:border-primary-500" required>
          </div>
          <div>
            <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">{{ t('admin.notifications.smtp.port') }}</label>
            <input type="number" v-model.number="platformForm.smtpPort" class="w-full px-3 py-2 text-sm rounded-md bg-gray-50 dark:bg-dark-700 border border-gray-200 dark:border-dark-600 focus:outline-none focus:ring-2 focus:ring-primary-500/30 focus:border-primary-500">
          </div>
          <div>
            <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">{{ t('admin.notifications.smtp.user') }}</label>
            <input type="text" v-model="platformForm.smtpUser" class="w-full px-3 py-2 text-sm rounded-md bg-gray-50 dark:bg-dark-700 border border-gray-200 dark:border-dark-600 focus:outline-none focus:ring-2 focus:ring-primary-500/30 focus:border-primary-500" required>
          </div>
          <div>
            <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">{{ t('admin.notifications.smtp.pass') }}</label>
            <input type="password" v-model="platformForm.smtpPass" class="w-full px-3 py-2 text-sm rounded-md bg-gray-50 dark:bg-dark-700 border border-gray-200 dark:border-dark-600 focus:outline-none focus:ring-2 focus:ring-primary-500/30 focus:border-primary-500" required>
          </div>
          <div>
            <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">{{ t('admin.notifications.smtp.from') }}</label>
            <input type="text" v-model="platformForm.smtpFrom" class="w-full px-3 py-2 text-sm rounded-md bg-gray-50 dark:bg-dark-700 border border-gray-200 dark:border-dark-600 focus:outline-none focus:ring-2 focus:ring-primary-500/30 focus:border-primary-500">
          </div>
          <div>
            <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">{{ t('admin.notifications.smtp.to') }}</label>
            <input type="text" v-model="platformForm.smtpTo" class="w-full px-3 py-2 text-sm rounded-md bg-gray-50 dark:bg-dark-700 border border-gray-200 dark:border-dark-600 focus:outline-none focus:ring-2 focus:ring-primary-500/30 focus:border-primary-500" required>
            <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.notifications.smtp.toHint') }}</p>
          </div>
	          <div class="flex items-center justify-between">
	            <label class="text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('admin.notifications.smtp.secure') }}</label>
	            <Toggle
	              :modelValue="platformForm.smtpSecure ?? false"
	              @update:modelValue="(value: boolean) => (platformForm.smtpSecure = value)"
	            />
	          </div>
	          <div class="flex items-center justify-between">
	            <label class="text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('admin.notifications.smtp.ignoreTLS') }}</label>
	            <Toggle
	              :modelValue="platformForm.smtpIgnoreTLS ?? false"
	              @update:modelValue="(value: boolean) => (platformForm.smtpIgnoreTLS = value)"
	            />
	          </div>
	        </div>

        <div v-if="platformForm.type && ['dingtalk', 'custom'].includes(platformForm.type as string)" class="space-y-4">
          <div v-if="platformForm.type === 'dingtalk'" class="flex items-center justify-between">
            <label class="text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('admin.notifications.enableSign') }}</label>
            <Toggle 
              :modelValue="platformForm.enableSign ?? false" 
              @update:modelValue="(value: boolean) => (platformForm.enableSign = value)"
            />
          </div>

          <div v-if="platformForm.type === 'custom' || (platformForm.type === 'dingtalk' && platformForm.enableSign)">
            <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">{{ t('admin.notifications.secret') }}</label>
            <input type="text" v-model="platformForm.secret" class="w-full px-3 py-2 text-sm rounded-md bg-gray-50 dark:bg-dark-700 border border-gray-200 dark:border-dark-600 focus:outline-none focus:ring-2 focus:ring-primary-500/30 focus:border-primary-500">
          </div>
        </div>

        <div class="flex justify-end gap-3 pt-4">
          <button type="button" @click="closePlatformModal" class="px-4 py-2 text-sm font-medium rounded-lg border border-gray-200 dark:border-dark-600 hover:bg-gray-100 dark:hover:bg-dark-700 transition-colors duration-150">{{ t('common.cancel') }}</button>
          <button type="submit" class="px-4 py-2 text-sm font-medium rounded-lg bg-primary-600 text-white hover:bg-primary-700 transition-colors duration-150">{{ t('common.save') }}</button>
        </div>
      </form>
    </BaseDialog>

     <!-- Delete Confirmation Dialog -->
    <ConfirmDialog
      :show="showDeleteConfirm"
      :title="t('admin.notifications.deletePlatform')"
      :message="t('admin.notifications.deleteConfirmMessage')"
      @confirm="deleteConfirmed"
      @cancel="showDeleteConfirm = false"
      danger
    />
  </AppLayout>
</template>
